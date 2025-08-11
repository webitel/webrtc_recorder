package model

type RtcUploadVideoSession interface {
	ID() string
	AnswerSDP() string
}
