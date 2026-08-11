// Package loudness makes everything the radio plays come out at the same
// perceived level, without touching how anything sounds.
//
// The problem it solves: a channel mixes a 2015 pop master (around -9 LUFS,
// squashed flat by the loudness war), a public-radio podcast (-18), and a 1955
// serial ripped off a transcription disc (-27). Fed straight into the encoder
// those are eighteen decibels apart, which is the difference between "too
// quiet to follow" and "reaching for the volume knob".
//
// The fix is the one broadcasters, ReplayGain, and every streaming service
// converged on, and it is deliberately the *boring* one:
//
//  1. Measure the item's integrated loudness once, offline (EBU R128 /
//     ITU-R BS.1770 — a K-weighted, gated average of the whole item).
//  2. Work out the single constant decibel offset that lands it on target.
//  3. Multiply the samples by that one number at playback.
//
// Step 3 is a static gain. It is one multiplication applied equally to every
// sample in the item, so the quiet parts stay exactly as far below the loud
// parts as the engineer left them. Nothing is compressed, nothing pumps,
// nothing breathes. A track's dynamics survive the process completely intact —
// the only thing that changes is where the whole track sits.
//
// This is emphatically NOT what ffmpeg's single-pass `loudnorm` or
// `dynaudnorm` do. Those are dynamic processors: they ride the gain within the
// item, which flattens a track's own light and shade and makes music pump
// audibly against a talk bed. They exist for a different job (levelling a
// stream you cannot measure in advance) and using them here is how a library
// ends up sounding uniformly loud and uniformly lifeless.
//
// The one exception is the peak limiter, and it is bounded on purpose. See
// Target.Plan.
package loudness

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Measurement is what an analysis pass found out about one item.
type Measurement struct {
	// IntegratedLUFS is gated, K-weighted loudness over the measured span:
	// the number that predicts how loud a human says something is. Typical
	// values run from about -6 (a brickwalled modern master) to about -30 (an
	// unrestored archive recording).
	IntegratedLUFS float64

	// TruePeakDBTP is the highest inter-sample peak, in dB relative to full
	// scale. It is not the same as the highest sample: reconstructing the
	// analogue waveform between two samples can overshoot both of them, which
	// is why a file that never exceeds 0 dBFS on paper can still clip a DAC.
	TruePeakDBTP float64

	// LoudnessRange is the spread between the item's quiet and loud passages,
	// in LU. Nothing in the policy reads it; it is stored because it is the
	// number that explains an odd-sounding decision after the fact.
	LoudnessRange float64

	// Partial marks a measurement taken from a window rather than the whole
	// item — the only option for a live stream, which has no end to measure
	// to. Partial measurements get a tighter boost ceiling because a sample of
	// a station is not a promise about the rest of it.
	Partial bool

	MeasuredAt time.Time
}

// Valid reports whether a measurement is usable.
//
// Two failure modes reach here as numbers rather than errors. ffmpeg reports
// silence (or something so quiet it is effectively silence) as roughly -70
// LUFS or lower; boosting that by the maximum would turn a file's noise floor
// into the loudest thing on the station. And a true peak that is absent or
// absurd means the analysis did not complete, in which case guessing is worse
// than doing nothing.
func (m Measurement) Valid() bool {
	if math.IsNaN(m.IntegratedLUFS) || math.IsInf(m.IntegratedLUFS, 0) {
		return false
	}
	if m.IntegratedLUFS <= -60 || m.IntegratedLUFS > 6 {
		return false
	}
	// A true peak is required, not optional. Every measurement this package
	// takes asks for one, so its absence means the pass did not complete
	// properly however cheerful the rest of the output looked.
	if !m.peakKnown() {
		return false
	}
	// Loudness cannot exceed peak. Integrated loudness is a gated average of
	// the signal and the true peak is its single loudest instant, so I <= TP
	// holds for every real recording — with equality only approached by a
	// full-scale square wave.
	//
	// This is here because of a specific, expensive failure. An ffmpeg too old
	// for an option in the filter chain emitted a *degenerate* summary — the
	// headings were all present, but the numbers were `I: 0.0` and
	// `Peak: -inf` — then exited non-zero. Every individual field looked
	// parseable and 0.0 sat inside the range check above, so ~19,000 files
	// were cached as "measured at 0.0 LUFS" and every one of them got a 16 dB
	// cut on air. Checking the numbers against each other is what catches a
	// broken pass that still produces well-formed output.
	const tolerance = 0.5
	return m.IntegratedLUFS <= m.TruePeakDBTP+tolerance
}

// peakKnown reports whether TruePeakDBTP can be trusted. When it cannot, the
// policy refuses to boost at all: without a peak there is no way to know how
// much headroom an item has, and a boost into the unknown is the one mistake
// that is actually audible as damage.
func (m Measurement) peakKnown() bool {
	if math.IsNaN(m.TruePeakDBTP) || math.IsInf(m.TruePeakDBTP, 0) {
		return false
	}
	return m.TruePeakDBTP > -60 && m.TruePeakDBTP < 12
}

// Target is the policy: where items should land and how far the normaliser is
// allowed to go to put them there.
type Target struct {
	// LUFS is the loudness every item is aimed at.
	LUFS float64

	// CeilingDBTP is the true-peak ceiling after gain.
	CeilingDBTP float64

	// MaxBoostDB caps how much a quiet item may be lifted.
	MaxBoostDB float64

	// MaxCutDB caps how far a loud item may be pulled down (a positive
	// number of decibels of attenuation).
	MaxCutDB float64

	// MaxLimitDB is how many decibels of peak reduction the limiter is
	// allowed to absorb. See Plan for why this is the important knob.
	MaxLimitDB float64

	// PartialBoostDB caps the boost applied from a partial (windowed)
	// measurement, such as a live stream's.
	PartialBoostDB float64
}

// DefaultTarget is tuned for a mixed music-and-talk station going into a hi-fi
// amplifier.
//
// -16 LUFS is the streaming and podcast convention (Apple Music, most podcast
// networks; Spotify sits a couple of dB louder). Broadcast EBU R128 calls for
// -23, which is correct for television and far too quiet for a device sharing
// an amplifier with everything else in the house.
//
// The practical consequence, stated plainly because it is the first thing
// anyone notices: modern masters get turned DOWN several dB, because they were
// mastered louder than anything else in the library. The station as a whole
// ends up a little quieter than its loudest material used to be and a lot
// louder than its quietest, which is the entire point — one setting of the
// volume knob now works for the whole day.
//
// -1 dBTP of headroom is not superstition. The channel re-encodes to MP3 or
// AAC after this gain is applied, and lossy codecs do not preserve peaks
// exactly: a signal mastered right up to 0 dBFS routinely decodes a fraction
// of a dB above it and clips on the way out.
var DefaultTarget = Target{
	LUFS:           -16,
	CeilingDBTP:    -1,
	MaxBoostDB:     12,
	MaxCutDB:       20,
	MaxLimitDB:     6,
	PartialBoostDB: 6,
}

// Plan is the decision for one item: a gain, and whether the limiter needs to
// be in circuit behind it.
type Plan struct {
	// GainDB is the constant offset applied to every sample.
	GainDB float64

	// Limit puts a true-peak limiter after the gain. False for the large
	// majority of items, where the gain alone fits under the ceiling.
	Limit bool

	// CeilingDBTP is the limiter's threshold, meaningful only when Limit.
	CeilingDBTP float64
}

// Zero reports a plan that changes nothing, so callers can skip the filter
// chain entirely rather than running audio through a no-op.
func (p Plan) Zero() bool { return p.GainDB == 0 && !p.Limit }

// gainDeadband is the point below which a correction is not worth making. Half
// a decibel is inaudible on programme material, and skipping it means items
// that were already on target pass through with no filter attached at all.
const gainDeadband = 0.5

// Plan works out what to do with a measured item.
//
// The whole design lives in one branch. Having computed the gain that would
// put the item on target, compare it against the gain the item's own peaks
// allow:
//
//   - If the wanted gain fits under the ceiling, apply it and stop. No
//     limiter, no compressor, no processing of any kind — the samples are
//     multiplied by a constant and that is the end of it. This is what happens
//     to most of a real library, because most material is being turned down.
//
//   - If it does not fit, the item is quiet on average but has tall isolated
//     peaks — a high crest factor, which in practice means orchestral,
//     acoustic, or an old dynamic recording. Reaching the target would need
//     the peaks held back, so the gain is capped at what the limiter is
//     allowed to absorb (MaxLimitDB) and the item is left a little under
//     target rather than squashed to reach it.
//
// That cap is the guarantee. No item can ever receive more than MaxLimitDB of
// peak reduction, so "normalised" can never degrade into "compressed". Leaving
// a dynamic recording a few dB below its neighbours is the right trade: it is
// a level difference nobody reaches for the volume knob over, and the
// alternative is flattening the one kind of material whose dynamics are the
// point.
//
// The branch is also self-selecting in the normaliser's favour. Dense,
// heavily-limited material measures loud, so its wanted gain is negative and
// it never gets near the limiter. Only high-crest material does — and high
// crest means the peaks poking above the ceiling are brief and isolated, which
// is precisely the case where a few dB of limiting cannot be heard.
func (t Target) Plan(m Measurement) Plan {
	if !m.Valid() {
		return Plan{}
	}
	target := t.normalized()

	wanted := target.LUFS - m.IntegratedLUFS
	boostCap := target.MaxBoostDB
	if m.Partial && target.PartialBoostDB < boostCap {
		boostCap = target.PartialBoostDB
	}
	if wanted > boostCap {
		wanted = boostCap
	}
	if wanted < -target.MaxCutDB {
		wanted = -target.MaxCutDB
	}

	// Without a trustworthy peak there is no headroom figure, so attenuation
	// is still safe but a boost is not.
	if !m.peakKnown() {
		if wanted > 0 {
			wanted = 0
		}
		return finish(wanted, false, target.CeilingDBTP)
	}

	// How much gain the item's own peaks leave room for. Frequently negative:
	// a master that already touches full scale has to come down to make the
	// ceiling regardless of how loud it measures.
	headroom := target.CeilingDBTP - m.TruePeakDBTP

	if wanted <= headroom {
		return finish(wanted, false, target.CeilingDBTP)
	}

	allowed := headroom + target.MaxLimitDB
	if allowed > wanted {
		allowed = wanted
	}
	// A limiter is only worth inserting if the gain actually pushes past the
	// ceiling once it has been capped.
	return finish(allowed, allowed > headroom, target.CeilingDBTP)
}

func finish(gain float64, limit bool, ceiling float64) Plan {
	gain = math.Round(gain*10) / 10
	if math.Abs(gain) < gainDeadband && !limit {
		return Plan{}
	}
	return Plan{GainDB: gain, Limit: limit, CeilingDBTP: ceiling}
}

// normalized fills in anything the caller left at zero, so a partially
// configured Target cannot silently mean "no headroom, no boost, no cut".
func (t Target) normalized() Target {
	if t.LUFS == 0 {
		t.LUFS = DefaultTarget.LUFS
	}
	if t.CeilingDBTP == 0 {
		t.CeilingDBTP = DefaultTarget.CeilingDBTP
	}
	if t.MaxBoostDB <= 0 {
		t.MaxBoostDB = DefaultTarget.MaxBoostDB
	}
	if t.MaxCutDB <= 0 {
		t.MaxCutDB = DefaultTarget.MaxCutDB
	}
	if t.MaxLimitDB < 0 {
		t.MaxLimitDB = DefaultTarget.MaxLimitDB
	}
	if t.PartialBoostDB <= 0 {
		t.PartialBoostDB = DefaultTarget.PartialBoostDB
	}
	return t
}

// FilterSpec renders the plan as an ffmpeg filtergraph for -af, or "" when
// there is nothing to do.
func (p Plan) FilterSpec() string {
	if p.Zero() {
		return ""
	}
	var parts []string
	if p.GainDB != 0 {
		parts = append(parts, fmt.Sprintf("volume=%.1fdB", p.GainDB))
	}
	if p.Limit {
		limit := math.Pow(10, p.CeilingDBTP/20)
		// level=disabled matters more than it looks. alimiter's default is to
		// normalise its output back up after limiting, which would undo the
		// gain decision this package just spent all that care making and hand
		// back exactly the "everything is loud now" result the static-gain
		// design exists to avoid.
		//
		// 5ms attack and 60ms release are transparent settings for catching
		// isolated peaks: fast enough to stop an overshoot, slow enough that
		// the gain reduction does not modulate the bass.
		parts = append(parts, fmt.Sprintf(
			"alimiter=limit=%.4f:attack=5:release=60:level=disabled", limit))
	}
	return strings.Join(parts, ",")
}

// Describe is a one-line summary for logs. Being able to read "-9.4 LUFS →
// -6.6 dB" out of a log is the difference between diagnosing a level problem
// and guessing at one.
func (p Plan) Describe(m Measurement) string {
	if p.Zero() {
		return fmt.Sprintf("%.1f LUFS, on target", m.IntegratedLUFS)
	}
	suffix := ""
	if p.Limit {
		suffix = fmt.Sprintf(" +limiter@%.1f dBTP", p.CeilingDBTP)
	}
	return fmt.Sprintf("%.1f LUFS peak %.1f dBTP → %+.1f dB%s",
		m.IntegratedLUFS, m.TruePeakDBTP, p.GainDB, suffix)
}
