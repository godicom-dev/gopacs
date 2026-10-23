package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/godicom-dev/gopacs/internal/archive"
	"github.com/godicom-dev/gopacs/internal/dimse"
	"github.com/godicom-dev/gopacs/internal/rest"
)

const version = "0.1.0"

func main() {
	aeTitle := flag.String("ae", "GOPACS", "called AE title")
	dicomAddr := flag.String("dicom", ":4242", "DIMSE listen address")
	httpAddr := flag.String("http", ":8042", "HTTP listen address")
	data := flag.String("data", "gopacs-data", "directory for the index and DICOM files")
	overwrite := flag.Bool("overwrite", false, "replace an instance that is already stored")
	maxUpload := flag.Int64("max-upload", 256<<20, "largest accepted HTTP body, in bytes")
	flag.Parse()

	store, err := archive.Open(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dicomLn, err := net.Listen("tcp", *dicomAddr)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		err := (dimse.Server{
			AETitle:   *aeTitle,
			Store:     store,
			Overwrite: *overwrite,
		}).Serve(ctx, dicomLn)
		if err != nil && ctx.Err() == nil {
			log.Printf("dicom: %v", err)
			stop()
		}
	}()

	api := rest.Handler{
		Store: store,
		System: rest.System{
			Name:               "gopacs",
			Version:            version,
			ApiVersion:         1,
			DicomAet:           *aeTitle,
			DicomPort:          portOf(dicomLn.Addr()),
			HttpPort:           portFromAddr(*httpAddr),
			OverwriteInstances: *overwrite,
		},
		Overwrite: *overwrite,
		MaxUpload: *maxUpload,
	}
	srv := &http.Server{
		Addr:              *httpAddr,
		Handler:           api.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("gopacs %s  DICOM %s (%s)  HTTP %s  data %s", version, dicomLn.Addr(), *aeTitle, *httpAddr, *data)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func portOf(addr net.Addr) int {
	if addr == nil {
		return 0
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func portFromAddr(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}
