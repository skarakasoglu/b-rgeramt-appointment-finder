package main

import (
	"burgeramt-appointment-finder/appointments"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
)

func askQuestion(question string, instructions string) string {
	fmt.Printf("\033[1m%s\033[0m\n", question)
	if instructions != "" {
		fmt.Println(instructions)
	}
	var input string
	fmt.Print("> \033[0m")
	fmt.Scanln(&input)
	return input
}

func main() {
	var (
		id    = flag.String("i", "", "A unique ID for your script. Used by the Berlin.de team to identify requests from you.")
		email = flag.String("e", "", "Your email address. Required by the Berlin.de team.")
		url   = flag.String("u", "", "URL to the service page on Berlin.de. For example, \"https://service.berlin.de/dienstleistung/120686/\"")
		quiet = flag.Bool("q", false, "Limit output to essential logging.")
		port  = flag.Int("p", 80, "Port to use.")
	)

	flag.Parse()

	if *quiet {
		log.SetOutput(os.Stdout)
	} else {
		log.SetOutput(ioutil.Discard)
	}

	servicePageURL := *url
	if servicePageURL == "" {
		servicePageURL = askQuestion(
			"What is the URL of the service you want to watch?",
			"This is the service.berlin.de page for the service you want an appointment for. For example, \"https://service.berlin.de/dienstleistung/120686/\"",
		)
	}

	userEmail := *email
	if userEmail == "" {
		userEmail = askQuestion(
			"What is your email address?",
			"It will be included in the requests this script makes. It's required by the Berlin.de appointments team.",
		)
	}

	appointments.WatchForAppointments(servicePageURL, userEmail, *id, *port, *quiet)
}
