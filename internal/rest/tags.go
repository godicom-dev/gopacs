package rest

import (
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
)

func parseTag(s string) (tag.Tag, error) {
	s = strings.NewReplacer("-", "", ",", "", "(", "", ")", "").Replace(s)
	return tag.Parse(s)
}

func simplifiedTags(ds *godicom.Dataset) map[string]any {
	return tagObject(ds)
}

func detailedTags(ds *godicom.Dataset) map[string]any {
	out := map[string]any{}
	if ds == nil {
		return out
	}
	for _, tg := range ds.SortedTags() {
		if skipTag(tg) {
			continue
		}
		elem, ok := ds.Get(tg)
		if !ok || elem == nil {
			continue
		}
		name, _ := tag.Keyword(tg)
		entry := map[string]any{"Name": name}
		if elem.VR == godicom.VRSQ {
			entry["Type"] = "Sequence"
			entry["Value"] = sequenceValue(ds, tg)
		} else if binaryVR(elem.VR) {
			continue
		} else {
			entry["Type"] = "String"
			value, ok := ds.GetString(tg)
			if !ok || value == "" {
				entry["Value"] = nil
			} else {
				entry["Value"] = value
			}
		}
		out[formatTag(tg)] = entry
	}
	return out
}

func tagObject(ds *godicom.Dataset) map[string]any {
	out := map[string]any{}
	if ds == nil {
		return out
	}
	for _, tg := range ds.SortedTags() {
		if skipTag(tg) {
			continue
		}
		elem, ok := ds.Get(tg)
		if !ok || elem == nil || binaryVR(elem.VR) {
			continue
		}
		name, known := tag.Keyword(tg)
		if !known {
			name = formatTag(tg)
		}
		if elem.VR == godicom.VRSQ {
			out[name] = sequenceValue(ds, tg)
			continue
		}
		value, ok := ds.GetString(tg)
		if !ok || value == "" {
			out[name] = nil
			continue
		}
		out[name] = value
	}
	return out
}

func sequenceValue(ds *godicom.Dataset, tg tag.Tag) []any {
	seq, ok := ds.GetSequence(tg)
	if !ok || seq == nil {
		return []any{}
	}
	out := make([]any, 0, len(seq.Items()))
	for _, item := range seq.Items() {
		out = append(out, tagObject(item))
	}
	return out
}

func skipTag(tg tag.Tag) bool {
	return tg == tag.PixelData || tg.Group() == 0x7FE0
}

func binaryVR(vr godicom.VR) bool {
	switch vr {
	case godicom.VROB, godicom.VROW, godicom.VROF, godicom.VROD, godicom.VROL, godicom.VROV, godicom.VRUN:
		return true
	default:
		return false
	}
}

func formatTag(tg tag.Tag) string {
	return strings.ToLower(leftPad(tg.Group()) + "," + leftPad(tg.Element()))
}

func leftPad(n int) string {
	const hexdigits = "0123456789abcdef"
	return string([]byte{hexdigits[(n>>12)&0xF], hexdigits[(n>>8)&0xF], hexdigits[(n>>4)&0xF], hexdigits[n&0xF]})
}
