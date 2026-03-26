package main

import (
	"fmt"
	"log"
	"os"

	"pulse/internal/buildinfo"
	"pulse/internal/server"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(buildinfo.Version)
		return
	}
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
