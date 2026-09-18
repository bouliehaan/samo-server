//go:build explolive

package explo

// Live blast-radius dry run for the evidence-aware recording choice
// (2026-09-17, "Creep kept as Klangsberg"): replays every matched row of a
// real ledger through the REAL AcoustID lookup with the file's REAL
// fingerprint and its own evidence — exactly what the deployed identify path
// sees — and reports which identities change. Nothing is written anywhere.
//
// Inputs (all required; the test skips without them):
//
//	SAMO_EXPLO_LIVE_LEDGER        TSV: track_id status score mb_rec mb_rg
//	                              matched_title matched_artist matched_album
//	                              tag_title tag_artist tag_album
//	                              duration_seconds external_ids_json path
//	SAMO_EXPLO_LIVE_FINGERPRINTS  TSV: track_id duration fingerprint (fpcalc)
//	SAMO_ACOUSTID_API_KEY         the server's AcoustID client key
//	SAMO_EXPLO_LIVE_REPORT        where to write the report
//
//	go test -tags explolive -run TestLiveReidentifyLedger -v ./internal/explo/

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveReidentifyLedger(t *testing.T) {
	ledgerPath := os.Getenv("SAMO_EXPLO_LIVE_LEDGER")
	fpPath := os.Getenv("SAMO_EXPLO_LIVE_FINGERPRINTS")
	key := os.Getenv("SAMO_ACOUSTID_API_KEY")
	reportPath := os.Getenv("SAMO_EXPLO_LIVE_REPORT")
	if ledgerPath == "" || fpPath == "" || key == "" || reportPath == "" {
		t.Skip("set SAMO_EXPLO_LIVE_LEDGER, SAMO_EXPLO_LIVE_FINGERPRINTS, SAMO_ACOUSTID_API_KEY and SAMO_EXPLO_LIVE_REPORT")
	}

	fingerprints := map[string]Fingerprint{}
	for _, record := range readTSV(t, fpPath, false) {
		if len(record) < 3 {
			continue
		}
		seconds, _ := strconv.Atoi(record[1])
		fingerprints[record[0]] = Fingerprint{DurationSeconds: seconds, Value: record[2]}
	}

	ctx := context.Background()
	report := &strings.Builder{}
	var checked, changed, contradicted, contradictedChanged, sameRecordingNewAlbum int
	seen := map[string]bool{}
	for _, record := range readTSV(t, ledgerPath, true) {
		if len(record) < 14 || seen[record[0]] {
			continue
		}
		seen[record[0]] = true
		fp, ok := fingerprints[record[0]]
		if !ok {
			fmt.Fprintf(report, "SKIP\t%s\tno fingerprint\n", record[13])
			continue
		}
		duration, _ := strconv.Atoi(record[11])
		score, _ := strconv.ParseFloat(record[2], 64)
		row := ledgerIdentity{
			candidate: candidateTrack{
				trackID: record[0], path: record[13], title: record[8], artist: record[9], album: record[10],
				durationSeconds: duration, musicBrainzRecordingID: embeddedRecordingID(record[12]),
			},
			status: record[1],
			match: identifiedTrack{
				MusicBrainzRecordingID: record[3], MusicBrainzReleaseGroupID: record[4],
				Title: record[5], Artist: record[6], Album: record[7], Score: score,
			},
		}
		if strings.EqualFold(row.candidate.album, "Weekly-Exploration") {
			row.candidate.album = "" // the drop folder's name, as isDropFolderName would blank it
		}
		if row.status != "matched" {
			fmt.Fprintf(report, "SKIP\t%s\t%s (text-search match, no AcoustID row to replay)\n", record[13], row.status)
			continue
		}
		wouldRecheck := identityContradicted(row)
		if wouldRecheck {
			contradicted++
		}

		time.Sleep(acoustidMinInterval)
		match, matched, err := lookupAcoustID(ctx, nil, key, fp, row.candidate.evidence())
		checked++
		switch {
		case err != nil:
			fmt.Fprintf(report, "ERROR\t%s\t%v\n", record[13], err)
			continue
		case !matched:
			fmt.Fprintf(report, "NOMATCH\t%s\tledger: %s / %s\n", record[13], row.match.Artist, row.match.Title)
			continue
		}
		differs := identityDiffers(row.match, match)
		nameChanged := strings.TrimSpace(match.Title) != strings.TrimSpace(row.match.Title) || strings.TrimSpace(match.Artist) != strings.TrimSpace(row.match.Artist)
		status := "SAME"
		if differs {
			changed++
			if wouldRecheck {
				contradictedChanged++
			}
			status = "CHANGED"
			if !nameChanged && match.MusicBrainzRecordingID == row.match.MusicBrainzRecordingID {
				status = "ALBUM-ONLY"
				sameRecordingNewAlbum++
			}
		}
		fmt.Fprintf(report, "%s\trecheck=%v\t%s\n\tledger: %s / %s [%s] rec=%s\n\tnew:    %s / %s [%s] rec=%s agrees-with-tags=%v\n",
			status, wouldRecheck, record[13],
			row.match.Artist, row.match.Title, row.match.Album, short(row.match.MusicBrainzRecordingID),
			match.Artist, match.Title, match.Album, short(match.MusicBrainzRecordingID), identityAgrees(match, row.candidate.evidence()))
	}

	summary := fmt.Sprintf("rows replayed: %d\nidentities that change under the new chooser: %d (of which album-only, same recording: %d)\nrows the boot-time identity check would re-check (contradicted by their file): %d, of which change: %d\n",
		checked, changed, sameRecordingNewAlbum, contradicted, contradictedChanged)
	if err := os.WriteFile(reportPath, []byte(summary+"\n"+report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + summary)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func readTSV(t *testing.T, path string, header bool) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records [][]string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		records = append(records, strings.Split(line, "\t"))
	}
	if header && len(records) > 0 {
		records = records[1:]
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i][0] < records[j][0] })
	return records
}
