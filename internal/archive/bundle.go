package archive

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"github.com/iter-dev/iter/internal/db/repo"
)

// SessionBundle is the in-memory shape we serialize per session.
// Stored on R2 as one tar.zst per session at
// `<tenant_id>/<yyyy-mm>/<session_id>.tar.zst`.
//
// JSON inside the tarball (one entry per child shape: session.json,
// events.json, embedding.json, scores.json, outcomes.json) so the
// archive is human-inspectable with stock tools (`tar -xOf foo.tar.zst
// session.json | jq .`). A single combined JSON would be simpler but
// loses the ability to grep individual shapes without unmarshaling the
// whole blob; the tarball overhead is ~100 bytes per entry, dominated
// by the payload size on any non-empty session.
type SessionBundle struct {
	Session   repo.Session
	Events    []repo.SessionEventRow
	Embedding *repo.Embedding // nullable: may not have been embedded
	Scores    []repo.Score
	Outcomes  []repo.Outcome
	Pointer   repo.ArchivePointer // post-archive; populated by caller before/after upload
	BundledAt time.Time           // when this bundle was constructed (UTC)
}

// ObjectKey returns the R2 object key for this bundle:
//
//	<tenant_id>/<yyyy-mm>/<session_id>.tar.zst
//
// Date partitioning is by the session's StartedAt (UTC, YYYY-MM) so a
// "show me everything from May 2026 under tenant X" query is a single
// list-objects prefix scan rather than a full bucket walk. The bucket
// is private + tenant-scoped at the access layer; the prefix is purely
// an indexing convenience.
func (b SessionBundle) ObjectKey() string {
	month := b.Session.StartedAt.UTC().Format("2006-01")
	return fmt.Sprintf("%s/%s/%s.tar.zst",
		b.Session.TenantID.String(), month, b.Session.ID.String())
}

// EncodeTarZstd serializes the bundle to a tar archive and zstd-compresses
// the whole archive (tar-then-compress, not zstd-of-individual-entries).
// Rationale: zstd's dictionary builds across the whole tar payload, so
// repeated JSON keys like "session_id" share encoding across entries —
// per-entry zstd would forgo that win.
//
// Compression level: Default (3). Higher levels add ~30%+ CPU for <5%
// additional savings on JSON; the cron is bottlenecked by R2 PutObject
// roundtrip, not CPU.
func EncodeTarZstd(b SessionBundle) ([]byte, error) {
	if b.Session.ID == uuid.Nil {
		return nil, errors.New("archive.EncodeTarZstd: empty session id")
	}

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)

	if err := writeJSON(tw, "session.json", b.Session, b.BundledAt); err != nil {
		return nil, err
	}
	if err := writeJSON(tw, "events.json", b.Events, b.BundledAt); err != nil {
		return nil, err
	}
	if b.Embedding != nil {
		if err := writeJSON(tw, "embedding.json", b.Embedding, b.BundledAt); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(tw, "scores.json", b.Scores, b.BundledAt); err != nil {
		return nil, err
	}
	if err := writeJSON(tw, "outcomes.json", b.Outcomes, b.BundledAt); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("archive.EncodeTarZstd: close tar: %w", err)
	}

	var zstdBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		return nil, fmt.Errorf("archive.EncodeTarZstd: new zstd: %w", err)
	}
	if _, err := enc.Write(tarBuf.Bytes()); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("archive.EncodeTarZstd: zstd write: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("archive.EncodeTarZstd: zstd close: %w", err)
	}
	return zstdBuf.Bytes(), nil
}

// writeJSON marshals v with indented output (so the archives are
// human-readable when extracted) and writes it to tw with a deterministic
// mtime so identical sessions encode to identical tar payloads — useful
// for content-hash based dedup checks downstream.
func writeJSON(tw *tar.Writer, name string, v any, mtime time.Time) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("archive.bundle: marshal %s: %w", name, err)
	}
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(body)),
		ModTime: mtime.UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("archive.bundle: write header %s: %w", name, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("archive.bundle: write body %s: %w", name, err)
	}
	return nil
}
