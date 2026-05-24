go build -o /tmp/samo ./cmd/samo-server
SAMO_FFMPEG_PATH=$(which ffmpeg) SAMO_FFPROBE_PATH=$(which ffprobe) \
  SAMO_DATA_DIR=/tmp/samo-data /tmp/samo
