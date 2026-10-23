// Package dimse accepts C-ECHO, C-STORE, C-FIND and C-MOVE and indexes them in an archive.
package dimse

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	dcmstatus "github.com/godicom-dev/gonetdicom/status"
	"github.com/godicom-dev/gopacs/internal/archive"
)

// Server is a Storage and Query/Retrieve SCP backed by Store.
type Server struct {
	AETitle   string
	Addr      string
	Store     *archive.Store
	Overwrite bool
}

// ListenAndServe accepts associations until ctx is cancelled.
func (s Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts associations on ln until ctx is cancelled.
func (s Server) Serve(ctx context.Context, ln net.Listener) error {
	accepted := make([]string, 0, len(ae.AllStorageSOPClasses)+4)
	accepted = append(accepted, ae.AllStorageSOPClasses...)
	accepted = append(accepted,
		ae.PatientRootQueryRetrieveInformationModelFind,
		ae.StudyRootQueryRetrieveInformationModelFind,
		ae.PatientRootQueryRetrieveInformationModelMove,
		ae.StudyRootQueryRetrieveInformationModelMove,
	)
	return ae.Serve(ctx, ln, ae.ServerConfig{
		AETitle:                  s.AETitle,
		AcceptedAbstractSyntaxes: accepted,
		OnCStore:                 s.onStore,
		OnCFind:                  s.onFind,
		OnCMove:                  s.onMove,
	})
}

func (s Server) onStore(_ context.Context, req ae.StoreRequest) uint16 {
	if req.Data == nil {
		return dcmstatus.CannotUnderstand
	}
	fd := &godicom.FileDataset{Dataset: req.Data, FileMeta: req.FileMeta}
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		return dcmstatus.CannotUnderstand
	}
	if _, err := s.Store.Put(raw, s.Overwrite); err != nil {
		if errors.Is(err, archive.ErrMissingIdentity) || errors.Is(err, archive.ErrNotDICOM) {
			return dcmstatus.CannotUnderstand
		}
		return dcmstatus.ProcessingFailure
	}
	return dcmstatus.Success
}

func (s Server) onFind(_ context.Context, req ae.FindRequest) []ae.FindMatch {
	level, query := identifierQuery(req.IdentifierData)
	if !levelAllowed(req.QueryModel, level) {
		return []ae.FindMatch{{Status: dcmstatus.IdentifierDoesNotMatchSOPClass}}
	}
	hits, err := s.Store.Find(level, query)
	if err != nil {
		return []ae.FindMatch{{Status: dcmstatus.UnableToProcess}}
	}
	out := make([]ae.FindMatch, 0, len(hits)+1)
	for _, hit := range hits {
		out = append(out, ae.FindMatch{
			Status:     dimse.StatusPending,
			Identifier: identifier(level, req.IdentifierData, hit.Tags),
		})
	}
	out = append(out, ae.FindMatch{Status: dimse.StatusSuccess})
	return out
}

// onMove is the evt.EVT_C_MOVE handler. The first result is the destination
// address, which pynetdicom yields before any match. An AE Title that is not
// in the modality table is an unknown destination.
func (s Server) onMove(_ context.Context, req ae.MoveRequest) ae.MovePlan {
	dest, err := s.Store.ModalityByAET(req.MoveDestination)
	if errors.Is(err, archive.ErrNotFound) {
		return ae.MovePlan{Destination: &ae.MoveDestination{}}
	}
	if err != nil {
		return ae.MovePlan{Responses: []ae.RetrieveMatch{{Status: dcmstatus.UnableToProcess}}}
	}
	known := &ae.MoveDestination{
		Addr:      net.JoinHostPort(dest.Host, strconv.Itoa(dest.Port)),
		CalledAE:  dest.AET,
		CallingAE: s.AETitle,
	}
	level, query := identifierQuery(req.IdentifierData)
	if !levelAllowed(req.QueryModel, level) {
		return ae.MovePlan{
			Destination: known,
			Responses:   []ae.RetrieveMatch{{Status: dcmstatus.IdentifierDoesNotMatchSOPClass}},
		}
	}
	hits, err := s.Store.Find(level, query)
	if err != nil {
		return ae.MovePlan{Destination: known, Responses: []ae.RetrieveMatch{{Status: dcmstatus.UnableToProcess}}}
	}
	var stores []ae.StoreRequest
	for _, hit := range hits {
		files, err := s.Store.ArchiveFiles(hit.Kind, hit.ID)
		if err != nil {
			return ae.MovePlan{Destination: known, Responses: []ae.RetrieveMatch{{Status: dcmstatus.UnableToProcess}}}
		}
		for _, file := range files {
			raw, err := os.ReadFile(file.Path)
			if err != nil {
				return ae.MovePlan{Destination: known, Responses: []ae.RetrieveMatch{{Status: dcmstatus.UnableToProcess}}}
			}
			store, err := storeRequest(raw)
			if err != nil {
				return ae.MovePlan{Destination: known, Responses: []ae.RetrieveMatch{{Status: dcmstatus.UnableToProcess}}}
			}
			stores = append(stores, store)
		}
	}
	return ae.MovePlan{Destination: known, Stores: stores}
}

func identifierQuery(ds *godicom.Dataset) (string, map[string]string) {
	query := map[string]string{}
	if ds == nil {
		return "", query
	}
	level, _ := ds.GetString(tag.QueryRetrieveLevel)
	for _, tg := range ds.SortedTags() {
		if tg == tag.QueryRetrieveLevel {
			continue
		}
		elem, ok := ds.Get(tg)
		if !ok || elem == nil || elem.VR == godicom.VRSQ {
			continue
		}
		name, ok := tag.Keyword(tg)
		if !ok {
			continue
		}
		value, _ := ds.GetString(tg)
		query[name] = value
	}
	return normalize(level), query
}

func levelAllowed(model, level string) bool {
	switch level {
	case archive.LevelPatient, archive.LevelStudy, archive.LevelSeries, archive.LevelImage:
	default:
		return false
	}
	if level == archive.LevelPatient && (model == ae.StudyRootQueryRetrieveInformationModelFind || model == ae.StudyRootQueryRetrieveInformationModelMove) {
		return false
	}
	return true
}

func identifier(level string, query *godicom.Dataset, tags map[string]string) *godicom.Dataset {
	out := godicom.NewDataset()
	_ = out.SetString(tag.QueryRetrieveLevel, level)
	if query == nil {
		return out
	}
	for _, tg := range query.SortedTags() {
		if tg == tag.QueryRetrieveLevel {
			continue
		}
		elem, ok := query.Get(tg)
		if !ok || elem == nil || elem.VR == godicom.VRSQ {
			continue
		}
		name, ok := tag.Keyword(tg)
		if !ok {
			continue
		}
		vr := elem.VR
		if vr == "" {
			vr = godicom.VRLO
		}
		out.Set(godicom.NewDataElement(tg, vr, tags[name]))
	}
	return out
}

func normalize(level string) string {
	switch level {
	case "PATIENT", "STUDY", "SERIES", "IMAGE":
		return level
	default:
		return ""
	}
}
