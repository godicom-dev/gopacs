package dimse

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestEchoStoreFind(t *testing.T) {
	st, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- (Server{AETitle: "GOPACS", Store: st}).Serve(ctx, ln)
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	assoc, err := ae.Dial(dialCtx, ae.Config{
		AETitle: "SCU",
		PresentationContexts: []ae.PresentationContext{
			{ID: 1, AbstractSyntax: pdu.VerificationSOPClass, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 3, AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
			{ID: 5, AbstractSyntax: ae.StudyRootQueryRetrieveInformationModelFind, TransferSyntaxes: []string{pdu.ImplicitVRLittleEndian}},
		},
	}, ln.Addr().String(), "GOPACS")
	if err != nil {
		t.Fatal(err)
	}
	defer assoc.Release(dialCtx)

	if err := assoc.CEcho(dialCtx); err != nil {
		t.Fatal(err)
	}

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
	stored, err := assoc.CStore(dialCtx, ae.StoreRequest{
		AffectedSOPClassUID:    string(uid.CTImageStorage),
		AffectedSOPInstanceUID: "1.2.9.1.1",
		Data:                   ds,
	})
	if err != nil || stored.Status != dimse.StatusSuccess {
		t.Fatalf("c-store %+v %v", stored, err)
	}

	query := godicom.NewDataset()
	_ = query.SetString(tag.QueryRetrieveLevel, "STUDY")
	_ = query.SetString(tag.PatientID, "P1")
	_ = query.SetString(tag.StudyInstanceUID, "")
	matches, err := assoc.CFind(dialCtx, ae.FindRequest{
		QueryModel:     ae.StudyRootQueryRetrieveInformationModelFind,
		IdentifierData: query,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Status != dimse.StatusPending || matches[1].Status != dimse.StatusSuccess {
		t.Fatalf("matches %+v", matches)
	}
	uid, ok := matches[0].Identifier.GetString(tag.StudyInstanceUID)
	if !ok || uid != "1.2.9" {
		t.Fatalf("study uid %q %v", uid, ok)
	}

	cancel()
	<-errCh
}
