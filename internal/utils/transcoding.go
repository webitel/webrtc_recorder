package utils

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/webitel/webrtc_recorder/internal/model"
)

var timeRegex = regexp.MustCompile(`time=([0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]+)`)

type Transcoding struct {
	scale  string
	l      int64
	stdin  io.WriteCloser
	stdout io.ReadCloser
	r      io.Reader
	cmd    *exec.Cmd
	end    bool
}

var (
	mkv = []string{
		"-nostdin",
		"-fflags", "+genpts",
		"-i", "pipe:0",
		"-c:v", "copy",
		"-c:a", "libopus",
		"-f", "matroska",
		"pipe:1",
	}

	mp4 = []string{
		"-nostdin",
		"-fflags", "+genpts", // генеруємо PTS, якщо нема
		"-i", "pipe:0",
		"-f", "mp4", // ⬅️ саме mkv
		"-movflags", "frag_keyframe+empty_moov",
		"pipe:1",
	}
)

func NewTranscoding(src io.ReadCloser, writer io.Writer) (*Transcoding, error) {
	cmdArgs := mp4

	cmd := exec.Command("ffmpeg", cmdArgs...)
	cmd.Stderr = os.Stderr // bind log stream to stderr

	cmd.Stdin = src
	cmd.Stdout = writer

	return &Transcoding{
		cmd: cmd,
	}, nil
}

// probeActualDuration returns the real playback duration of a media file by
// decoding it through ffmpeg. This is necessary for IVF files where the
// container header stores frame-count metadata, not actual last-PTS duration.
func probeActualDuration(path string) (float64, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("ffmpeg",
		"-v", "quiet",
		"-stats",
		"-i", path,
		"-c", "copy",
		"-f", "null",
		os.DevNull,
	)
	cmd.Stderr = &stderr
	_ = cmd.Run()
	ms := parseDurationFromFFmpeg(stderr.String())
	if ms <= 0 {
		return 0, fmt.Errorf("could not parse duration from ffmpeg output")
	}
	return float64(ms) / 1000.0, nil
}

func transcodingArgs(src []model.MediaChannel, videoScale float64) ([]string, []string) {
	if len(src) == 0 {
		return nil, nil
	}

	var videoChannels []int
	var audioChannels []int

	// Розділяємо вхідні потоки на відео та аудіо
	for i, s := range src {
		if strings.HasPrefix(s.MimeType, "video") {
			videoChannels = append(videoChannels, i)
		} else if strings.HasPrefix(s.MimeType, "audio") {
			audioChannels = append(audioChannels, i)
		}
	}

	videoCount := len(videoChannels)
	audioCount := len(audioChannels)

	if videoCount == 0 && audioCount == 0 {
		return nil, nil
	}

	var inputArgs []string
	var filterComplexBuilder strings.Builder
	var finalMapArgs []string

	for i := 0; i < len(src); i++ {
		inputArgs = append(inputArgs, "-i", src[i].Path)
	}

	setPts := ""
	if math.Abs(videoScale-1.0) > 0.001 {
		setPts = fmt.Sprintf("setpts=%.6f*PTS,", videoScale)
	}

	if videoCount > 0 {
		if videoCount == 1 {
			filterComplexBuilder.WriteString(fmt.Sprintf("[%d:v]%sscale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2[v_out];", videoChannels[0], setPts))
		} else {
			cols := int(math.Ceil(math.Sqrt(float64(videoCount))))
			rows := int(math.Ceil(float64(videoCount) / float64(cols)))

			const finalWidth = 1920
			const finalHeight = 1080

			windowWidth := finalWidth / cols
			windowHeight := finalHeight / rows

			var inputStreamsForXstack []string
			for i, videoIdx := range videoChannels {
				streamName := fmt.Sprintf("v_norm_%d", i)
				filter := fmt.Sprintf(
					"[%d:v]%sscale=%d:%d:force_original_aspect_ratio=decrease[v_scaled_%d]; "+
						"[v_scaled_%d]pad=%d:%d:(ow-iw)/2:(oh-ih)/2[%s]; ",
					videoIdx, setPts, windowWidth, windowHeight, i,
					i, windowWidth, windowHeight, streamName,
				)
				filterComplexBuilder.WriteString(filter)
				inputStreamsForXstack = append(inputStreamsForXstack, fmt.Sprintf("[%s]", streamName))
			}

			var layout []string
			for r := 0; r < rows; r++ {
				for c := 0; c < cols; c++ {
					index := r*cols + c
					if index >= videoCount {
						break
					}
					posX := c * windowWidth
					posY := r * windowHeight
					layout = append(layout, fmt.Sprintf("%d_%d", posX, posY))
				}
			}

			inputStreams := strings.Join(inputStreamsForXstack, "")
			layoutString := strings.Join(layout, "|")

			xstackFilter := fmt.Sprintf(
				"%sxstack=inputs=%d:layout=%s[v_out];",
				inputStreams, videoCount, layoutString,
			)
			filterComplexBuilder.WriteString(xstackFilter)
		}
		finalMapArgs = append(finalMapArgs, "-map", "[v_out]")
	}

	if audioCount > 0 {
		for _, audioIdx := range audioChannels {
			filterComplexBuilder.WriteString(fmt.Sprintf("[%d:a]", audioIdx))
		}
		filterComplexBuilder.WriteString(fmt.Sprintf("amix=inputs=%d[a_out]", audioCount))
		finalMapArgs = append(finalMapArgs, "-map", "[a_out]")
	}

	finalFilter := strings.TrimSuffix(filterComplexBuilder.String(), ";")
	finalArgs := append([]string{"-filter_complex", finalFilter}, finalMapArgs...)

	return inputArgs, finalArgs
}

func TranscodingByPath(src []model.MediaChannel, dst string, actualDurationMs int) (int, error) {
	var videoScale = 1.0

	for _, ch := range src {
		dur, err := probeActualDuration(ch.Path)
		if err == nil {
			fmt.Printf("[Transcoding] actual duration %s (%s): %.3fs\n", ch.MimeType, ch.Path, dur)
		} else {
			fmt.Printf("[Transcoding] actual duration %s (%s): error: %v\n", ch.MimeType, ch.Path, err)
		}

		if actualDurationMs > 0 && strings.HasPrefix(ch.MimeType, "video") && err == nil && dur > 0.5 {
			actualDurSec := float64(actualDurationMs) / 1000.0
			videoScale = actualDurSec / dur
			fmt.Printf("[Transcoding] video actual=%.3fs ivf=%.3fs scale=%.6f\n",
				actualDurSec, dur, videoScale)
		}
	}

	args := []string{
		"-nostdin",
		"-threads", "1",
	}

	inputArgs, finalArgs := transcodingArgs(src, videoScale)
	args = append(args, inputArgs...)

	if finalArgs == nil {
		return 0, nil
	}
	args = append(args, finalArgs...)

	args = append(args,
		"-c:a", "aac",
		"-b:a", "192k",

		"-c:v", "libx264",
		"-tune", "animation",
		"-preset", "fast",
		"-movflags",
		"+faststart",
		"-f", "mp4",
		dst)

	cmd := exec.Command("ffmpeg", args...)

	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

	err := cmd.Start()
	if err != nil {
		return 0, err
	}

	err = cmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("ffmpeg error: %w, trace: %s", err, stderr.String())
	}
	durationMs := parseDurationFromFFmpeg(stderr.String())

	return durationMs, nil
}

func (t *Transcoding) Start() error {
	return t.cmd.Start()
}

func (t *Transcoding) Wait() error {
	return t.cmd.Wait()
}

func (t *Transcoding) Close() (err error) {
	if t.stdin != nil {
		err = t.stdin.Close() // close the stdin, or ffmpeg will wait forever
		if err != nil {
			return err
		}
	}

	err = t.cmd.Wait() // wait until ffmpeg finish
	if err != nil {
		return err
	}

	return nil
}

// parseDurationFromFFmpeg шукає останнє входження time= у виводі ffmpeg
func parseDurationFromFFmpeg(output string) int {
	matches := timeRegex.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return 0
	}

	lastMatch := matches[len(matches)-1][1]

	parts := strings.Split(lastMatch, ":")
	if len(parts) == 3 {
		durationStr := fmt.Sprintf("%sh%sm%ss", parts[0], parts[1], parts[2])
		d, err := time.ParseDuration(durationStr)
		if err == nil {
			return int(d.Milliseconds())
		}
	}
	return 0
}
