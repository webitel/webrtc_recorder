package model

import (
	"time"

	"github.com/google/uuid"
)

func NewId() string {
	id, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}
	return id.String()
}

func GetMillis() int {
	return int(time.Now().UnixNano() / int64(time.Millisecond))
}
