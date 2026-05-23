package radio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func (s *Service) Stream(ctx context.Context, stationID string, startedAt time.Time, dst io.Writer) error {
	s.mu.RLock()
	station, ok := s.stations[normalizeID(stationID)]
	s.mu.RUnlock()
	if !ok {
		return ErrStationNotFound
	}

	cursor := startedAt.UTC()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		slot, item, err := station.slotAt(cursor)
		if err != nil {
			return err
		}
		if err := streamItem(ctx, dst, item, slot.OffsetSeconds); err != nil {
			return err
		}

		cursor = time.Now().UTC()
		if cursor.Before(slot.EndsAt) {
			cursor = slot.EndsAt
		}
	}
}

func streamItem(ctx context.Context, dst io.Writer, item mediaItem, offsetSeconds int) error {
	file, err := os.Open(item.path)
	if err != nil {
		return fmt.Errorf("open %q: %w", item.path, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", item.path, err)
	}
	if stat.Size() == 0 {
		return nil
	}

	startByte := approximateByteOffset(stat.Size(), item.durationSeconds, offsetSeconds)
	if startByte > 0 {
		if _, err := file.Seek(startByte, io.SeekStart); err != nil {
			return fmt.Errorf("seek %q: %w", item.path, err)
		}
	}

	remainingSeconds := item.durationSeconds - offsetSeconds
	if remainingSeconds <= 0 {
		remainingSeconds = item.durationSeconds
	}
	remainingBytes := stat.Size() - startByte
	bytesPerSecond := remainingBytes / int64(remainingSeconds)
	if bytesPerSecond <= 0 {
		bytesPerSecond = 1
	}

	return copyThrottled(ctx, dst, file, bytesPerSecond)
}

func approximateByteOffset(size int64, durationSeconds int, offsetSeconds int) int64 {
	if size <= 0 || durationSeconds <= 0 || offsetSeconds <= 0 {
		return 0
	}
	if offsetSeconds >= durationSeconds {
		return 0
	}
	return size * int64(offsetSeconds) / int64(durationSeconds)
}

func copyThrottled(ctx context.Context, dst io.Writer, src io.Reader, bytesPerSecond int64) error {
	buffer := make([]byte, 32*1024)
	startedAt := time.Now()
	var written int64

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, readErr := src.Read(buffer)
		if n > 0 {
			wrote, writeErr := dst.Write(buffer[:n])
			written += int64(wrote)
			if writeErr != nil {
				return writeErr
			}
			if wrote != n {
				return io.ErrShortWrite
			}

			expectedElapsed := time.Duration(written * int64(time.Second) / bytesPerSecond)
			if sleepFor := startedAt.Add(expectedElapsed).Sub(time.Now()); sleepFor > 0 {
				timer := time.NewTimer(sleepFor)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
