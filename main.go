package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"text/template"
)

func main() {
	fociCount := flag.Int("foci-count", 4, "How many FOCI should be started")
	fociName := flag.String("foci-name", "localhost", "Name of this FOCI. Used to determine if a cascing request should be made")
	httpAddress := flag.String("http-address", ":8080", "Address for frontend for FOCI cluster")
	flag.Parse()

	fociPorts := []int{}
	for i := 0; i < *fociCount; i++ {
		fociPort, err := createFoci(*fociName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Starting FOCI on port %d ", fociPort)

		fociPorts = append(fociPorts, fociPort)
	}

	log.Print("Serving HTTP on " + *httpAddress)
	fileServer := &http.Server{
		Addr: *httpAddress,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			indexHTML, err := ioutil.ReadFile("static/index.html")
			if err != nil {
				panic(err)
			}

			if err := template.Must(template.New("").Parse(string(indexHTML))).Execute(w, fociPorts); err != nil {
				log.Fatal(err)
			}

		}),
	}
	log.Fatal(fileServer.ListenAndServe())
}

func createFoci(fociName string) (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}

	fociPort := listener.Addr().(*net.TCPAddr).Port
	go func() {
		fociWebRTCServer := &foci{
			name: fmt.Sprintf("%s:%d", fociName, fociPort),
		}
		fociHTTPServer := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				setCorsHeaders(w)

				switch {
				case r.URL.String() == "/createSession" && r.Method == "POST":
					if err := fociWebRTCServer.handleCreateSession(w, r); err != nil {
						log.Fatal(err)
					}
				}
			}),
		}

		log.Fatal(fociHTTPServer.Serve(listener))
	}()

	return fociPort, nil
}

type dataChannelMessage struct {
	Event    string `json:"event"`
	Message  string `json:"message,omitempty"`
	ID       string `json:"id"`
	CallID   string `json:"call_id"`
	DeviceID string `json:"device_id"`
	Purpose  string `json:"purpose"`
	SDP      string `json:"sdp"`
	FOCI     string `json:"foci"`
}

func setCorsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization,X-CSRF-Token")
	w.Header().Set("Access-Control-Expose-Headers", "Authorization")
}
