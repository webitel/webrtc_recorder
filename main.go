package main

import (
	"fmt"
	"github.com/webitel/webrtc_recorder/cmd"
)

//go:generate go run github.com/google/wire/cmd/wire@latest gen ./cmd
func main() {
	if err := cmd.Run(); err != nil {
		fmt.Println(err.Error())
		return
	}
}
