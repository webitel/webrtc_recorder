package utils

import (
	"fmt"
	"github.com/webitel/webrtc_recorder/internal/model"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
)

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

func transcodingArgs(src []model.MediaChannel) ([]string, []string) {
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

	if videoCount > 0 {
		if videoCount == 1 {
			filterComplexBuilder.WriteString(fmt.Sprintf("[%d:v]scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2[v_out];", videoChannels[0]))
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
					"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease[v_scaled_%d]; "+
						"[v_scaled_%d]pad=%d:%d:(ow-iw)/2:(oh-ih)/2[%s]; ",
					videoIdx, windowWidth, windowHeight, i,
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

func TranscodingByPath(src []model.MediaChannel, dst string) error {
	args := []string{
		"-nostdin",
		"-threads", "1",
	}

	inputArgs, finalArgs := transcodingArgs(src)
	args = append(args, inputArgs...)

	if finalArgs == nil {
		return nil
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
	//cmd.Stderr = os.Stderr // bind log stream to stderr
	err := cmd.Start()
	if err != nil {
		return err
	}

	return cmd.Wait()
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
