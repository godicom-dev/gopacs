// Package dimse accepts C-ECHO, C-STORE and C-FIND and indexes them in an archive.
package dimse

import (
	"context"
	"errors"
	"net"

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
	accepted := make([]string, 0, len(ae.AllStorageSOPClasses)+2)
	accepted = append(accepted, ae.AllStorageSOPClasses...)
	accepted = append(accepted,
		ae.PatientRootQueryRetrieveInformationModelFind,
		ae.StudyRootQueryRetrieveInformationModelFind,
	)
	return ae.Serve(ctx, ln, ae.ServerConfig{
		AETitle:                  s.AETitle,
		AcceptedAbstractSyntaxes: accepted,
		OnCStore:                 s.onStore,
		OnCFind:                  s.onFind,
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
	level := ""
	if req.IdentifierData != nil {
		level, _ = req.IdentifierData.GetString(tag.QueryRetrieveLevel)
	}
	level = normalize(level)
	if level == "" {
		return []ae.FindMatch{{Status: dcmstatus.IdentifierDoesNotMatchSOPClass}}
	}
	if level == archive.LevelPatient && req.QueryModel == ae.StudyRootQueryRetrieveInformationModelFind {
		return []ae.FindMatch{{Status: dcmstatus.IdentifierDoesNotMatchSOPClass}}
	}
	query := map[string]string{}
	if req.IdentifierData != nil {
		for _, tg := range req.IdentifierData.SortedTags() {
			if tg == tag.QueryRetrieveLevel {
				continue
			}
			elem, ok := req.IdentifierData.Get(tg)
			if !ok || elem == nil || elem.VR == godicom.VRSQ {
				continue
			}
			name, ok := tag.Keyword(tg)
			if !ok {
				continue
			}
			value, _ := req.IdentifierData.GetString(tg)
			query[name] = value
		}
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
