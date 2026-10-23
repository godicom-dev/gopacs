# gopacs

A single-binary DICOM archive. It stores Part 10 files, answers C-ECHO, C-STORE and C-FIND, and exposes the Orthanc REST paths needed to upload, list and download them.

godicom reads and writes the datasets. gonetdicom speaks DIMSE. There is no C compiler and no plugin to install: the codecs travel with godicom.

```bash
go install github.com/godicom-dev/gopacs/cmd/gopacs@latest
gopacs
```

DIMSE listens on `:4242` as AE title `GOPACS`. HTTP listens on `:8042`. Files and the SQLite index go in `./gopacs-data`.

```bash
gopacs -ae GOPACS -dicom :4242 -http :8042 -data /var/lib/gopacs
```

## REST

Resource identifiers are Orthanc identifiers: the SHA-1 of the DICOM identity tags, shown as 40 hex characters grouped `8-8-8-8-8`. Patient ID, then Study, Series and SOP Instance UID, joined with `|`.

| Method | Path | |
|--------|------|--|
| GET | `/system` | name, version, AE title, ports |
| POST | `/instances` | upload one Part 10 file |
| GET | `/patients` `/studies` `/series` `/instances` | identifiers; `?expand` returns the resources |
| GET | `/patients/{id}` and the same for studies, series, instances | one resource |
| GET | `/patients/{id}/studies`, `/studies/{id}/series`, `/studies/{id}/instances`, `/series/{id}/instances` | children |
| GET | `/studies/{id}/patient`, `/series/{id}/study`, `/series/{id}/patient`, `/instances/{id}/series`, `/instances/{id}/study`, `/instances/{id}/patient` | the parent resource |
| GET | `/instances/{id}/file` | the stored Part 10 file |
| GET | `/instances/{id}/simplified-tags` `/tags` `/content/{group-element}` | the dataset |
| POST | `/tools/lookup` | body is a DICOM UID or an Orthanc identifier |
| POST | `/tools/find` | `{"Level":"Study","Query":{"PatientID":"..."},"Expand":true}` |
| DELETE | `/patients/{id}` and the same for studies, series, instances | deletes that resource and everything under it |

A second upload of the same instance returns `"Status": "AlreadyStored"` and keeps the first file. `-overwrite` replaces it.

C-FIND accepts the Patient Root and Study Root models. An empty query value matches anything. `*` and `?` are wildcards, and a `YYYYMMDD-YYYYMMDD` value is a date range.

## Not in this version

Worklists, anonymization, modalities and peers, the changes feed, C-MOVE and C-GET, DICOMweb, and a web viewer. This is not Orthanc; the routes above are the part a script needs in order to store and fetch.
