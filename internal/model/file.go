package model

import (
	"encoding/json"
)

type File struct {
	DomainId   int    `json:"domain_id"`
	UploadedBy int    `json:"uploaded_by"`
	CreatedAt  int    `json:"created_at"`
	MimeType   string `json:"mime_type"`
	Name       string `json:"name"`
	Uuid       string `json:"uuid"`
	Path       string `json:"path"`
	Channel    int    `json:"channel"`
}

func (f *File) Json() []byte {
	js, _ := json.Marshal(f)

	return js
}
