package main

import "log"

func main() {
	if err := validate(); err != nil {
		log.Fatal(err)
	}
	log.Print("server ready")
}
