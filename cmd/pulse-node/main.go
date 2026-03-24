package main

import (
	"log"

	"pulse/internal/node"
)

func main() {
	if err := node.Run(); err != nil {
		log.Fatal(err)
	}
}
