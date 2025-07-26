package model

type RtcUploadVideoSession interface {
	Id() string
	AnswerSDP() string
}
