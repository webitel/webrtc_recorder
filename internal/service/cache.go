package service

import (
	"errors"
	"fmt"
	"github.com/webitel/webrtc_recorder/config"
	"github.com/webitel/webrtc_recorder/internal/model"
	"io"
	"os"
	"path"
	"path/filepath"
)

type CacheService struct {
	dir string
}

func NewCacheService(cfg *config.Config) *CacheService {
	if _, err := os.Stat(cfg.TempDir); os.IsNotExist(err) {
		err = os.MkdirAll(cfg.TempDir, 0755)
		if err != nil {
			panic(err)
		}
	}

	dir, err := filepath.Abs(cfg.TempDir)
	if err != nil {
		panic(err)
	}

	return &CacheService{
		dir: dir,
	}
}

func (svc *CacheService) DeleteFile(file *model.File) error {
	if file.Path == "" {
		return errors.New("file path is empty")
	}

	return os.Remove(file.Path)
}

func (svc *CacheService) NewReader(file model.File) (io.ReadCloser, error) {
	return os.Open(file.Path)
}

func (svc *CacheService) NewWriter(file *model.File, ext string) (io.WriteCloser, error) {
	if file.Path != "" {
		return nil, errors.New("file path is no empty")
	}
	name := model.NewId()
	if ext != "" {
		name += "." + ext
	}
	file.Path = path.Join(svc.dir, fmt.Sprintf("%s", name))
	return os.OpenFile(file.Path, os.O_WRONLY|os.O_CREATE, 0644)
}
