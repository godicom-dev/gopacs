# gopacs

A single-binary DICOM archive. It stores Part 10 files, answers C-ECHO, C-STORE, C-FIND and C-MOVE, and exposes the Orthanc REST paths needed to upload, list, download and send them.

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
| GET | `/statistics` | counts and stored bytes, also `/{patients,studies,series,instances}/{id}/statistics` |
| GET | `/changes` | `?since=&limit=`; `DELETE /changes` clears the log |
| GET | `/patients/{id}/archive` and the same for studies, series, instances | a ZIP of the DICOM files |
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
| GET | `/modalities` | names; `?expand` returns each `{AET, Host, Port}` |
| PUT | `/modalities/{name}` | `{"AET","Host","Port"}` creates or replaces that modality |
| GET, DELETE | `/modalities/{name}` | one modality |
| POST | `/modalities/{name}/echo` | C-ECHO |
| POST | `/modalities/{name}/store` | C-STORE the resources in the body. A JSON string, an array of Orthanc identifiers, or `{"Resources":[...]}`. The call finishes before the response |

A second upload of the same instance returns `"Status": "AlreadyStored"` and keeps the first file. `-overwrite` replaces it.

C-FIND accepts the Patient Root and Study Root models. An empty query value matches anything. `*` and `?` are wildcards, and a `YYYYMMDD-YYYYMMDD` value is a date range.

## Page

Open `http://localhost:8042/`. The browser is sent to `/app/patients`. Each list and resource is a normal URL rendered by the server: `/app/patients`, `/app/studies`, `/app/patients/{id}`, `/app/studies/{id}`, `/app/series/{id}`, `/app/instances/{id}`, `/app/upload`, `/app/lookup`, and `/app/modalities`. `explorer.js` only runs the delete and upload buttons. A resource page can C-STORE that resource to a modality saved on `/app/modalities`.

A study page links to `/ohif/viewer?StudyInstanceUIDs=<StudyInstanceUID>`, the same URL the Orthanc OHIF plugin opens. `-ohif` is the built `platform/app/dist` directory (default `ohif-dist`). When `index.html` is there, gopacs serves it under `/ohif/` and replaces `app-config.js` so OHIF uses `/dicom-web`. The route stays unregistered until that build is present. The page does not render pixels itself.

## DICOMweb

The same archive answers DICOMweb under `/dicom-web`:

| Transaction | Path |
|-------------|------|
| STOW-RS | `POST /dicom-web/studies` and `POST /dicom-web/studies/{StudyInstanceUID}` |
| QIDO-RS | `GET /dicom-web/studies`, `/studies/{study}/series`, `/studies/{study}/series/{series}/instances` |
| WADO-RS | retrieve a study, series, or instance; `/metadata`; `/frames/{n}`; `/rendered`; `/bulkdata` |

QIDO matches the queried value exactly. A viewer points its DICOMweb root at `http://host:8042/dicom-web`.

## Not in this version

Worklists, anonymization, Orthanc peers, and C-GET. C-MOVE sends to an AE Title saved as a modality. This is not Orthanc; the routes above are the part a script needs in order to store, fetch and send. Image display is the OHIF viewer, and only when `-ohif` points at a built dist.
