package utils

import (
	"io"
	"os"
	"os/exec"
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

func TranscodingByPath(src, dst string) error {
	args := []string{
		"-nostdin",
		"-i", src,
		"-vf", "scale=1920:1080",
		"-c:v", "libx264",
		"-tune", "animation",
		"-preset", "fast",
		"-movflags",
		"+faststart",
		"-f", "mp4",
		dst,
	}

	cmd := exec.Command("ffmpeg", args...)
	// cmd.Stderr = os.Stderr // bind log stream to stderr
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
