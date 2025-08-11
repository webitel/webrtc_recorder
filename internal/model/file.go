package model

import (
	"encoding/json"
)

type File struct {
	DomainID   int    `json:"domain_id"`
	UploadedBy int    `json:"uploaded_by"`
	CreatedAt  int    `json:"created_at"`
	MimeType   string `json:"mime_type"`
	Name       string `json:"name"`
	UUID       string `json:"uuid"`
	Path       string `json:"path"`
	Channel    int    `json:"channel"`
}

func (f *File) JSON() []byte {
	js, _ := json.Marshal(f)

	return js
}
