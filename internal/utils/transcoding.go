package utils

import (
	"fmt"
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

func transcodingArgs(src []string) ([]string, []string) {
	N := len(src)
	if N == 0 {
		return nil, nil
	}

	cols := int(math.Ceil(math.Sqrt(float64(N))))
	rows := int(math.Ceil(float64(N) / float64(cols)))

	const finalWidth = 1920
	const finalHeight = 1080

	windowWidth := finalWidth / cols
	windowHeight := finalHeight / rows

	var inputArgs []string
	var filterComplexBuilder strings.Builder
	var inputStreamsForXstack []string // Список [v_norm_0], [v_norm_1], ...

	for i := 0; i < N; i++ {
		inputArgs = append(inputArgs, "-i", src[i])
		streamName := fmt.Sprintf("v_norm_%d", i)
		filter := fmt.Sprintf(
			"[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease[v_scaled_%d]; "+
				"[v_scaled_%d]pad=%d:%d:(ow-iw)/2:(oh-ih)/2[%s]; ",
			i, windowWidth, windowHeight, i,
			i, windowWidth, windowHeight, streamName,
		)
		filterComplexBuilder.WriteString(filter)
		inputStreamsForXstack = append(inputStreamsForXstack, fmt.Sprintf("[%s]", streamName))
	}

	if N == 1 {
		return inputArgs, nil
	}

	var layout []string
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			index := r*cols + c
			if index >= N {
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
		"%sxstack=inputs=%d:layout=%s[v_out]",
		inputStreams, N, layoutString,
	)
	filterComplexBuilder.WriteString(xstackFilter)

	var finalArgs []string

	finalArgs = append(finalArgs,
		"-filter_complex", filterComplexBuilder.String(),
		"-map", "[v_out]", // Вихідний відеопотік
	)

	return inputArgs, finalArgs
}

func TranscodingByPath(src []string, dst string) error {
	args := []string{
		"-nostdin",
		"-threads", "1",
	}
	inputArgs, finalArgs := transcodingArgs(src)
	args = append(args, inputArgs...)

	if finalArgs != nil {
		args = append(args, finalArgs...)
	}

	args = append(args,
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
