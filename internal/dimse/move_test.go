package dimse

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestCMoveToModality(t *testing.T) {
	st, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Put(movePart10(t), false); err != nil {
		t.Fatal(err)
	}

	storeLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	moveLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stored atomic.Int32
	errCh := make(chan error, 2)
	go func() {
		errCh <- ae.Serve(ctx, storeLn, ae.ServerConfig{
			AETitle:                  "STORESCP",
			AcceptedAbstractSyntaxes: []string{string(uid.CTImageStorage)},
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				stored.Add(1)
				if req.MoveOriginatorApplicationEntityTitle != "QRSCU" {
					t.Errorf("originator %q", req.MoveOriginatorApplicationEntityTitle)
				}
				return dimse.StatusSuccess
			},
		})
	}()
	go func() {
		errCh <- (Server{AETitle: "GOPACS", Store: st}).Serve(ctx, moveLn)
	}()

	_, port, err := net.SplitHostPort(storeLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	if err := st.PutModality(archive.Modality{Name: "dest", AET: "STORESCP", Host: "127.0.0.1", Port: n}); err != nil {
		t.Fatal(err)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "QRSCU",
		PresentationContexts: []ae.PresentationContext{{
			ID:               1,
			AbstractSyntax:   ae.StudyRootQueryRetrieveInformationModelMove,
			TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian},
		}},
	}, moveLn.Addr().String(), "GOPACS")
	if err != nil {
		t.Fatal(err)
	}
	defer assoc.Release(dialCtx)

	query := godicom.NewDataset()
	_ = query.SetString(tag.QueryRetrieveLevel, "STUDY")
	_ = query.SetString(tag.StudyInstanceUID, "1.2.9")
	matches, err := assoc.CMove(dialCtx, ae.MoveRequest{
		QueryModel:      ae.StudyRootQueryRetrieveInformationModelMove,
		MoveDestination: "STORESCP",
		IdentifierData:  query,
	})
	if err != nil {
		t.Fatal(err)
	}
	last := matches[len(matches)-1]
	if last.Status != dimse.StatusSuccess || last.SubOperations.Completed != 1 || stored.Load() != 1 {
		t.Fatalf("move status 0x%04x completed %d stored %d", last.Status, last.SubOperations.Completed, stored.Load())
	}

	unknown, err := assoc.CMove(dialCtx, ae.MoveRequest{
		QueryModel:      ae.StudyRootQueryRetrieveInformationModelMove,
		MoveDestination: "NOWHERE",
		IdentifierData:  query,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 || unknown[0].Status != dcmstatus.MoveDestinationUnknown {
		t.Fatalf("unknown %+v", unknown)
	}

	cancel()
	<-errCh
	<-errCh
}

func movePart10(t *testing.T) []byte {
	t.Helper()
	ds := godicom.NewDataset()
	for _, pair := range []struct {
		tg tag.Tag
		v  string
	}{
		{tag.PatientID, "P1"},
		{tag.PatientName, "DOE^JANE"},
		{tag.StudyInstanceUID, "1.2.9"},
		{tag.SeriesInstanceUID, "1.2.9.1"},
		{tag.SOPInstanceUID, "1.2.9.1.1"},
		{tag.SOPClassUID, string(uid.CTImageStorage)},
		{tag.Modality, "CT"},
	} {
		if err := ds.SetString(pair.tg, pair.v); err != nil {
			t.Fatal(err)
		}
	}
	fd := &godicom.FileDataset{Dataset: ds, FileMeta: godicom.NewFileMetaDataset()}
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
