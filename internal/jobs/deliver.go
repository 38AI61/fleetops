package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/lib/pq"

	"fleetops/internal/apierr"
	"fleetops/internal/auth"
)

// Deliver limits and codes (GF-199, GF-200). The 413 and 503 codes extend the
// GF-178 vocabulary; they live here because internal/apierr is outside this phase.
const (
	// maxPhotoBytes is the proof photo cap: 5 MiB (D-25, GF-199).
	maxPhotoBytes = 5 * 1024 * 1024
	// deliverBodyLimit caps the whole deliver request, multipart framing included (GF-199a).
	deliverBodyLimit = "8M"

	codePayloadTooLarge   = "payload_too_large"
	codeProofsUnavailable = "proofs_unavailable"
)

// errTooLarge marks a photo part over maxPhotoBytes. writeErr maps it to 413.
var errTooLarge = errors.New("jobs: payload too large")

// ProofStore is the consumer-side seam for proof photos (GF-201). *proofs.Store satisfies it.
type ProofStore interface {
	Put(ctx context.Context, fleetID, jobID int64, r io.Reader, size int64, ext string) (string, error)
	PresignGet(ctx context.Context, key string) (string, error)
}

// jobView is the GF-173 job object plus proof_photo_url (GF-196). It is built per
// response and never stored. The URL is signed on read.
type jobView struct {
	Job
	ProofPhotoURL *string `json:"proof_photo_url"`
}

// views wraps jobs for a response. proof_photo_url is non-null iff the job has a
// proof photo. With no proof store configured, every URL is null (GF-196).
func (h *Handler) views(ctx context.Context, fleetID int64, js ...Job) ([]jobView, error) {
	out := make([]jobView, len(js))
	ids := make([]int64, len(js))
	for i, j := range js {
		out[i].Job = j
		ids[i] = j.ID
	}
	if h.Proofs == nil || len(js) == 0 {
		return out, nil
	}
	keys, err := proofKeys(ctx, h.Svc.DB, fleetID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		key, ok := keys[out[i].ID]
		if !ok {
			continue
		}
		url, err := h.Proofs.PresignGet(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("presign proof: %w", err)
		}
		out[i].ProofPhotoURL = &url
	}
	return out, nil
}

// proofKeys reads proof_photo for the listed jobs of the fleet. A job without a
// proof is absent from the map.
func proofKeys(ctx context.Context, q querier, fleetID int64, ids []int64) (map[int64]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, proof_photo FROM jobs WHERE fleet_id = $1 AND id = ANY($2) AND proof_photo IS NOT NULL`,
		fleetID, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("proof keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, fmt.Errorf("scan proof key: %w", err)
		}
		out[id] = key
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proof rows: %w", err)
	}
	return out, nil
}

// deliverForm is the parsed multipart body of POST /driver/jobs/:id/deliver.
// tooLarge is set instead of failing mid-read, so the checks run in GF-200 order.
type deliverForm struct {
	photo    []byte
	tooLarge bool
	lat, lng float64
}

// readDeliverForm reads the multipart body (GF-200). The photo part is read with
// io.LimitReader(max+1), so an oversize part is seen without a second pass. The
// other fields are still read so the GF-200 order holds.
func readDeliverForm(c echo.Context) (deliverForm, error) {
	mr, err := c.Request().MultipartReader()
	if err != nil {
		return deliverForm{}, ErrValidation
	}
	var (
		form             deliverForm
		havePhoto        bool
		haveLat, haveLng bool
		latText, lngText string
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return deliverForm{}, bodyErr(err)
		}
		switch part.FormName() {
		case "photo":
			if !havePhoto {
				b, err := io.ReadAll(io.LimitReader(part, maxPhotoBytes+1))
				if err != nil {
					return deliverForm{}, bodyErr(err)
				}
				form.photo = b
				form.tooLarge = len(b) > maxPhotoBytes
				havePhoto = true
			}
		case "lat":
			if !haveLat {
				v, err := readField(part)
				if err != nil {
					return deliverForm{}, bodyErr(err)
				}
				latText, haveLat = v, true
			}
		case "lng":
			if !haveLng {
				v, err := readField(part)
				if err != nil {
					return deliverForm{}, bodyErr(err)
				}
				lngText, haveLng = v, true
			}
		default:
			if _, err := io.Copy(io.Discard, part); err != nil {
				return deliverForm{}, bodyErr(err)
			}
		}
	}
	if !havePhoto || !haveLat || !haveLng {
		return deliverForm{}, ErrValidation
	}
	if form.lat, err = parseCoord(latText, 90); err != nil {
		return deliverForm{}, err
	}
	if form.lng, err = parseCoord(lngText, 180); err != nil {
		return deliverForm{}, err
	}
	return form, nil
}

// readField reads a small text part. The cap keeps a text field from growing.
func readField(part io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, 64))
	return string(b), err
}

// parseCoord parses one coordinate. NaN and Inf are refused, and the value must
// lie within [-limit, limit] (GF-200, GF-173).
func parseCoord(s string, limit float64) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < -limit || v > limit {
		return 0, ErrValidation
	}
	return v, nil
}

// bodyErr maps a body read failure. Echo's route BodyLimit reports 413 as an
// HTTPError; anything else is a malformed multipart body.
func bodyErr(err error) error {
	var he *echo.HTTPError
	if errors.As(err, &he) && he.Code == http.StatusRequestEntityTooLarge {
		return errTooLarge
	}
	return ErrValidation
}

// photoExt sniffs the first 512 bytes and returns the extension for a JPEG, PNG
// or WebP (GF-199). The declared Content-Type is ignored.
func photoExt(photo []byte) (string, error) {
	head := photo
	if len(head) > 512 {
		head = head[:512]
	}
	switch http.DetectContentType(head) {
	case "image/jpeg":
		return "jpg", nil
	case "image/png":
		return "png", nil
	case "image/webp":
		return "webp", nil
	}
	return "", ErrValidation
}

// deliver serves POST /driver/jobs/:id/deliver (GF-200). The route group has
// already refused non-drivers before any body read. Then: id, proof store,
// driver identity, the pre-upload gate (GF-197), body validation, upload (not on
// replay, GF-198), and the transaction. A replay still validates the body and
// then stores nothing (GF-200).
func (h *Handler) deliver(c echo.Context) error {
	p, ok := auth.PrincipalFrom(c)
	if !ok {
		return apierr.Write(c, http.StatusUnauthorized, apierr.Unauthorized)
	}
	id, ok := idParam(c)
	if !ok {
		return apierr.Write(c, http.StatusNotFound, apierr.NotFound)
	}
	if h.Proofs == nil {
		return apierr.Write(c, http.StatusServiceUnavailable, codeProofsUnavailable)
	}
	ctx := c.Request().Context()
	driverID, linked, err := resolveDriver(ctx, h.Svc.DB, p)
	if err != nil {
		return err
	}
	if !linked {
		return writeErr(c, ErrNotFound)
	}
	state, err := h.Svc.DriverDeliverGate(ctx, p.FleetID, id, driverID)
	if err != nil {
		return writeErr(c, err)
	}
	form, err := readDeliverForm(c)
	if err != nil {
		return writeErr(c, err)
	}
	if form.tooLarge {
		return writeErr(c, errTooLarge)
	}
	ext, err := photoExt(form.photo)
	if err != nil {
		return writeErr(c, err)
	}

	// Upload only when the job still needs a proof. A replay has one already.
	key := ""
	if state == StatePickedUp {
		key, err = h.Proofs.Put(ctx, p.FleetID, id, bytes.NewReader(form.photo), int64(len(form.photo)), ext)
		if err != nil {
			return fmt.Errorf("store proof: %w", err)
		}
	}
	ch, out, err := h.Svc.DriverDeliver(ctx, p.UserID, p.FleetID, id, driverID, key, form.lat, form.lng)
	if err != nil {
		return writeErr(c, err)
	}
	if out == Applied {
		publishChange(h.Events, ch)
	}
	views, err := h.views(ctx, p.FleetID, ch.Job)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, views[0])
}
