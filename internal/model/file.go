package model

import (
	"encoding/json"
)

//-f avfoundation -framerate 15  -i "1:none" -f avfoundation -filter_complex "[0:v]scale=1280:720[v0];[1:v]scale=1280:720[v1];[v0][v1] hstack=inputs=2" -c:v libx264 -y ./1.mp4

type File struct {
	DomainID   int      `json:"domain_id"`
	UploadedBy int      `json:"uploaded_by"`
	CreatedAt  int      `json:"created_at"`
	MimeType   string   `json:"mime_type"`
	Name       string   `json:"name"`
	UUID       string   `json:"uuid"`
	Path       string   `json:"path"`
	Track      []string `json:"track"`
	Channel    int      `json:"channel"`
	StartTime  int      `json:"start_time"`
	EndTime    int      `json:"end_time"`
}

func (f *File) JSON() []byte {
	js, _ := json.Marshal(f)

	return js
}
