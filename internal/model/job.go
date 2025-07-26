package model

import "encoding/json"

type JobState uint

const (
	JobIdle JobState = iota
	JobActive
)

type JobConfig struct {
}

type Job struct {
	Id     int        `json:"id" db:"id"`
	Type   string     `json:"type" db:"type"`
	File   *File      `json:"file" db:"file"`
	Config *JobConfig `json:"config" db:"config"`
	Retry  int        `json:"retry" db:"retry"`
}

func (j *JobConfig) Json() []byte {
	js, _ := json.Marshal(j)
	return js
}
