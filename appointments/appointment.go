package appointments

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gorilla/websocket"
)

var (
	connectedClients = make(map[*websocket.Conn]struct{})
	clientsMutex     sync.Mutex
	lastMessage      = Message{
		Time:                    datetimeToJSON(time.Now()),
		Status:                  200,
		AppointmentDates:        []string{},
		LastAppointmentsFoundOn: nil,
	}
	refreshDelay = 180 // Minimum allowed by Berlin.de's IKT-ZMS team.
	timezone     = mustLoadLocation("Europe/Berlin")
)

// Message represents the structure of the message sent to clients
type Message struct {
	Time                    string   `json:"time"`
	Status                  int      `json:"status"`
	AppointmentDates        []string `json:"appointmentDates"`
	Message                 string   `json:"message"`
	LastAppointmentsFoundOn *string  `json:"lastAppointmentsFoundOn"`
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Fatalf("Error loading location: %v", err)
	}
	return loc
}

func datetimeToJSON(datetimeObj time.Time) string {
	return datetimeObj.Format("2006-01-02T15:04:05Z")
}

func getHeaders(email string, scriptID string) map[string][]string {
	return map[string][]string{
		"Accept":                    []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Upgrade-Insecure-Requests": []string{"1"},
		"User-Agent":                []string{fmt.Sprintf("Mozilla/5.0 AppointmentBookingTool/1.1 (https://github.com/skarakasoglu/burgeramt-appointment-finder; %s; %s)", email, scriptID)},
		"Accept-Language":           []string{"en-gb"},
		"Accept-Encoding":           []string{"gzip, deflate"},
		"Connection":                []string{"keep-alive"},
	}
}

func getAppointmentsURL(servicePageURL string) string {
	trimmed := strings.TrimSuffix(servicePageURL, "/")
	splitted := strings.Split(trimmed, "/")
	serviceID := splitted[len(splitted)-1]
	return fmt.Sprintf("https://service.berlin.de/terminvereinbarung/termin/all/%s/", serviceID)
}

func parseAppointmentDates(pageContent string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageContent))
	if err != nil {
		fmt.Printf("Error parsing document: %v/n", err)
		return nil
	}

	var appointmentDates []string
	doc.Find("td.buchbar a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			timestampStr := strings.TrimSuffix(path.Base(href), "/")
			timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
			if err == nil {
				appointmentTime := time.Unix(timestamp, 0).In(timezone)
				appointmentDates = append(appointmentDates, datetimeToJSON(appointmentTime))
			}
		}
	})

	return appointmentDates
}

func getAppointments(appointmentsURL string, email string, scriptID string) ([]string, error) {
	today := time.Now().In(timezone)
	nextMonth := time.Date(today.Year(), (today.Month()%12)+1, 1, 0, 0, 0, 0, timezone)
	nextMonthTimestamp := nextMonth.Unix()

	client := &http.Client{}
	req, err := http.NewRequest("GET", appointmentsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	req.Header = getHeaders(email, scriptID)
	responsePage1, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching appointments page 1: %v", err)
	}
	defer responsePage1.Body.Close()

	// Read the response body and convert it to a string
	body, err := ioutil.ReadAll(responsePage1.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	page1Dates := parseAppointmentDates(string(body))

	page2URL := fmt.Sprintf("https://service.berlin.de/terminvereinbarung/termin/day/%d/", nextMonthTimestamp)
	req, err = http.NewRequest("GET", page2URL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request for page 2: %v", err)
	}

	req.Header = getHeaders(email, scriptID)
	responsePage2, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching appointments page 2: %v", err)
	}
	defer responsePage2.Body.Close()

	// Read the response body and convert it to a string
	body2, err := ioutil.ReadAll(responsePage2.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}
	page2Dates := parseAppointmentDates(string(body2))

	appointments := append(page1Dates, page2Dates...)
	return appointments, nil
}

func lookForAppointments(appointmentsURL string, email string, scriptID string, quiet bool) Message {
	var result Message

	appointments, err := getAppointments(appointmentsURL, email, scriptID)
	if err != nil {
		result = Message{
			Time:             datetimeToJSON(time.Now()),
			Status:           502,
			Message:          fmt.Sprintf("Could not fetch results from Berlin.de - %v", err),
			AppointmentDates: []string{},
		}
	} else {
		fmt.Printf("Found %d appointments: %v\n", len(appointments), appointments)
		if len(appointments) > 0 && !quiet {
			beep()
		}
		result = Message{
			Time:             datetimeToJSON(time.Now()),
			Status:           200,
			Message:          "",
			AppointmentDates: appointments,
		}
	}

	return result
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all connections by default
		return true
	},
}

func WatchForAppointments(servicePageURL string, email string, scriptID string, serverPort int, quiet bool) {
	fmt.Printf("Getting appointment URL for %s\n", servicePageURL)
	appointmentsURL := getAppointmentsURL(servicePageURL)
	fmt.Printf("URL found: %s\n", appointmentsURL)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("Error upgrading connection to WebSocket: %v\n", err)
			return
		}
		defer ws.Close()

		clientsMutex.Lock()
		connectedClients[ws] = struct{}{}
		clientsMutex.Unlock()

		// Send the latest results to the newly connected client
		err = ws.WriteJSON(lastMessage)
		if err != nil {
			fmt.Printf("Error writing JSON to WebSocket: %v\n", err)
			return
		}

		// Wait for the client to close the connection
		_, _, err = ws.ReadMessage()
		if err != nil {
			fmt.Printf("Error reading message from WebSocket: %v\n", err)
		}

		clientsMutex.Lock()
		delete(connectedClients, ws)
		clientsMutex.Unlock()
	})

	go func() {
		err := http.ListenAndServe(fmt.Sprintf(":%d", serverPort), nil)
		if err != nil {
			log.Fatalf("Error starting server: %v", err)
		}
	}()

	fmt.Printf("Server is running on port %d. Looking for appointments every %d seconds.\n", serverPort, refreshDelay)

	for {
		lastApptsFoundOn := lastMessage.LastAppointmentsFoundOn
		lastMessage = lookForAppointments(appointmentsURL, email, scriptID, quiet)

		if len(lastMessage.AppointmentDates) > 0 {
			now := time.Now().In(timezone)
			*lastMessage.LastAppointmentsFoundOn = now.Format(time.RFC3339)
		} else {
			lastMessage.LastAppointmentsFoundOn = lastApptsFoundOn
		}

		clientsMutex.Lock()
		for client := range connectedClients {
			err := client.WriteJSON(lastMessage)
			if err != nil {
				fmt.Printf("Error writing JSON to WebSocket: %v\n", err)
			}
		}
		clientsMutex.Unlock()

		time.Sleep(time.Second * time.Duration(refreshDelay))
	}
}

func beep() {
	cmd := exec.Command("beep") // Replace with your system's sound command or use a beep package
	err := cmd.Run()
	if err != nil {
		fmt.Printf("Error playing beep sound: %v\n", err)
	}
}
