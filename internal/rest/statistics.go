package rest

import (
	"net/http"
	"strconv"

	"github.com/godicom-dev/gopacs/internal/archive"
)

func (h Handler) statistics(w http.ResponseWriter, r *http.Request) {
	st, err := h.Store.GlobalStatistics()
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, statsJSON(st))
}

func (h Handler) resourceStatistics(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := h.Store.ResourceStatistics(kind, r.PathValue("id"))
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, statsJSON(st))
	}
}

func statsJSON(st archive.Stats) map[string]any {
	const megabyte = 1024 * 1024
	mb := st.Disk / megabyte
	size := strconv.FormatInt(st.Disk, 10)
	if st.Global {
		return map[string]any{
			"CountPatients": st.Patients, "CountStudies": st.Studies,
			"CountSeries": st.Series, "CountInstances": st.Instances,
			"TotalDiskSize": size, "TotalDiskSizeMB": mb,
			"TotalUncompressedSize": size, "TotalUncompressedSizeMB": mb,
		}
	}
	out := map[string]any{
		"DiskSize": size, "DiskSizeMB": mb,
		"UncompressedSize": size, "UncompressedSizeMB": mb,
		"DicomDiskSize": size, "DicomDiskSizeMB": mb,
		"DicomUncompressedSize": size, "DicomUncompressedSizeMB": mb,
	}
	if st.ShowStudies {
		out["CountStudies"] = st.Studies
	}
	if st.ShowSeries {
		out["CountSeries"] = st.Series
	}
	if st.ShowInstances {
		out["CountInstances"] = st.Instances
	}
	return out
}
