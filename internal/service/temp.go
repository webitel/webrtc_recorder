package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/internal/model"
)

type TempFileService struct {
	dir string
}

func NewTempFileService(cfg *config.Config) *TempFileService {
	if _, err := os.Stat(cfg.TempDir); os.IsNotExist(err) {
		err = os.MkdirAll(cfg.TempDir, 0o755)
		if err != nil {
			panic(err)
		}
	}

	dir, err := filepath.Abs(cfg.TempDir)
	if err != nil {
		panic(err)
	}

	return &TempFileService{
		dir: dir,
	}
}

func (svc *TempFileService) DeleteFile(file *model.File) error {
	if file.Path == "" {
		return errors.New("file path is empty")
	}

	return os.Remove(file.Path)
}

func (svc *TempFileService) NewReader(file model.File) (io.ReadCloser, error) {
	return os.Open(file.Path)
}

func (svc *TempFileService) NewWriter(file *model.File, ext string) (io.WriteCloser, error) {
	err := svc.NewFilePath(file, ext)
	if err != nil {
		return nil, err
	}

	return os.OpenFile(file.Path, os.O_WRONLY|os.O_CREATE, 0o644)
}

func (svc *TempFileService) NewFilePath(file *model.File, ext string) error {
	if file.Path != "" {
		return errors.New("file path is no empty")
	}

	name := model.NewId()
	if ext != "" {
		name += "." + ext
	}

	file.Path = path.Join(svc.dir, name)

	return nil
}
