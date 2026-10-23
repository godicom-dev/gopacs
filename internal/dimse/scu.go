package dimse

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gopacs/internal/archive"
)

// uncompressedSyntaxes is pynetdicom DEFAULT_TRANSFER_SYNTAXES, the list
// build_context uses for Verification when no syntax is given.
var uncompressedSyntaxes = []string{
	pdu.ImplicitVRLittleEndian,
	"1.2.840.10008.1.2.1",
	"1.2.840.10008.1.2.1.99",
	"1.2.840.10008.1.2.2",
}

// Echo sends C-ECHO to m. callingAE is this archive's AE title.
func Echo(ctx context.Context, callingAE string, m archive.Modality) error {
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle:     callingAE,
		DialTimeout: 5 * time.Second,
		PresentationContexts: []ae.PresentationContext{{
			AbstractSyntax:   pdu.VerificationSOPClass,
			TransferSyntaxes: uncompressedSyntaxes,
		}},
	}, net.JoinHostPort(m.Host, strconv.Itoa(m.Port)), m.AET)
	if err != nil {
		return err
	}
	defer func() { _ = assoc.Release(ctx) }()
	return assoc.CEcho(ctx)
}

// SendResult counts C-STORE sub-operations performed by Send.
type SendResult struct {
	Completed int
	Failed    int
}

// Send stores each Part 10 file at m. The association proposes one context
// per SOP Class and stored transfer syntax, the same set qrscp builds for a
// C-MOVE sub-operation.
func Send(ctx context.Context, callingAE string, m archive.Modality, part10s [][]byte) (SendResult, error) {
	var result SendResult
	if len(part10s) == 0 {
		return result, nil
	}
	requests := make([]ae.StoreRequest, 0, len(part10s))
	var contexts []ae.PresentationContext
	seen := map[string]struct{}{}
	for _, raw := range part10s {
		req, err := storeRequest(raw)
		if err != nil {
			result.Failed++
			continue
		}
		requests = append(requests, req)
		ts := req.TransferSyntax
		if ts == "" {
			ts = pdu.ImplicitVRLittleEndian
		}
		key := req.AffectedSOPClassUID + "\x00" + ts
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		contexts = append(contexts, ae.PresentationContext{
			AbstractSyntax:   req.AffectedSOPClassUID,
			TransferSyntaxes: []string{ts},
		})
	}
	if len(requests) == 0 {
		return result, nil
	}
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle:              callingAE,
		DialTimeout:          5 * time.Second,
		PresentationContexts: contexts,
	}, net.JoinHostPort(m.Host, strconv.Itoa(m.Port)), m.AET)
	if err != nil {
		return result, err
	}
	defer func() { _ = assoc.Release(ctx) }()
	for _, req := range requests {
		res, err := assoc.CStore(ctx, req)
		if err != nil || res == nil || res.Status != dimse.StatusSuccess {
			result.Failed++
			continue
		}
		result.Completed++
	}
	return result, nil
}

func storeRequest(part10 []byte) (ae.StoreRequest, error) {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return ae.StoreRequest{}, err
	}
	sop, ok := fd.Dataset.GetString(tag.SOPClassUID)
	inst, okInst := fd.Dataset.GetString(tag.SOPInstanceUID)
	if !ok || !okInst || sop == "" || inst == "" {
		return ae.StoreRequest{}, fmt.Errorf("dicom: missing SOP Class or SOP Instance UID")
	}
	var ts string
	if uid, ok := fd.TransferSyntaxUID(); ok {
		ts = string(uid)
	}
	return ae.StoreRequest{
		AffectedSOPClassUID:    sop,
		AffectedSOPInstanceUID: inst,
		TransferSyntax:         ts,
		Data:                   fd.Dataset,
	}, nil
}
