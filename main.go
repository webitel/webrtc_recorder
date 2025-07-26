package main

import (
	"fmt"
	"github.com/webitel/webrtc_recorder/cmd"
)

//go:generate go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf/buf.gen.engine.yaml
//go:generate go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf/buf.gen.storage.yaml
//go:generate go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf/buf.gen.webitel.yaml
//go:generate go run github.com/bufbuild/buf/cmd/buf@latest generate --template buf/buf.gen.yaml

//go:generate go run github.com/google/wire/cmd/wire@latest gen ./cmd
func main() {
	if err := cmd.Run(); err != nil {
		fmt.Println(err.Error())
		return
	}
}
