package main

import (
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"text/template"
)

func main() {
	fociCount := flag.Int("foci-count", 4, "How many FOCI should be started")
	httpAddress := flag.String("http-address", ":8080", "Address for frontend for FOCI cluster")
	flag.Parse()

	fociPorts := []int{}
	for i := 0; i < *fociCount; i++ {
		fociPort, err := createFoci()
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

func createFoci() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}

	fociServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setCorsHeaders(w)
			if r.URL.String() != "/createSession" || r.Method != "POST" {
				return
			}

			if err := handleCreateSession(w, r); err != nil {
				log.Fatal(err)
			}

		}),
	}

	go func() {
		log.Fatal(fociServer.Serve(listener))
	}()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

type dataChannelMessage struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	CallID   string `json:"call_id"`
	DeviceID string `json:"device_id"`
	Purpose  string `json:"purpose"`
	SDP      string `json:"sdp"`
}

func setCorsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization,X-CSRF-Token")
	w.Header().Set("Access-Control-Expose-Headers", "Authorization")
}
