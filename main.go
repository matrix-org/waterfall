package main

import (
	"flag"
	"log"
	"net"
	"net/http"
)

func main() {
	fociCount := flag.Int("foci-count", 4, "How many FOCI should be started")
	flag.Parse()

	for i := 0; i < *fociCount; i++ {
		if err := createFoci(); err != nil {
			log.Fatal(err)
		}
	}

	log.Print("Serving HTTP on port 8080")
	fileServer := &http.Server{
		Addr:    ":8080",
		Handler: http.FileServer(http.Dir("static")),
	}
	log.Fatal(fileServer.ListenAndServe())
}

func createFoci() error {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return err
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

	log.Printf("Starting FOCI on port %d ", listener.Addr().(*net.TCPAddr).Port)
	return nil
}

type dataChannelMessage struct {
	Event    string `json:"event"`
	ID       string `json:"id"`
	CallID   string `json:"call_id"`
	DeviceID string `json:"device_id"`
	Purpose  string `json:"purpose"`
}

func setCorsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization,X-CSRF-Token")
	w.Header().Set("Access-Control-Expose-Headers", "Authorization")
}
