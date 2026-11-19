package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"syscall"

	"poketokenbar/helper/internal/companion"
)

// Save export, import and snapshots (protocol §7.7). They run on the owner goroutine like every
// command, so the mutating ones (importSave, createSnapshot, restoreSnapshot) apply in arrival
// order, and only this process, which holds the state-dir lock, writes the save.

var snapshotID = regexp.MustCompile(`^companion-snapshot-[0-9-]+\.json$`)

type exportReply struct {
	Path    string            `json:"path"`
	Bytes   int               `json:"bytes"`
	Summary companion.Summary `json:"summary"`
}

type inspectReply struct {
	Digest       string            `json:"digest"`
	Schema       int               `json:"schema"`
	AppVersion   string            `json:"appVersion"`
	ExportedAt   string            `json:"exportedAt"`
	SourceDevice string            `json:"sourceDevice"`
	Incoming     companion.Summary `json:"incoming"`
	Current      companion.Summary `json:"current"`
}

type summaryReply struct {
	Summary companion.Summary `json:"summary"`
}

type snapshotsReply struct {
	Snapshots []companion.SnapshotSummary `json:"snapshots"`
}

type snapshotReply struct {
	Snapshot companion.SnapshotSummary `json:"snapshot"`
}

// saveCommand serves exportSave, inspectSave, importSave, listSnapshots, createSnapshot and
// restoreSnapshot.
func (d *daemon) saveCommand(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	switch r.Cmd {
	case "exportSave":
		path := a.absPath("path")
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		n, err := d.store.ExportSave(path)
		if d.replySaveErr(r.ID, err) {
			return
		}
		d.reply(r.ID, exportReply{Path: path, Bytes: n, Summary: d.store.Summary()})
	case "inspectSave":
		path := a.absPath("path")
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		data, env, err := companion.ReadSave(path)
		if d.replySaveErr(r.ID, err) {
			return
		}
		d.reply(r.ID, inspectReply{Digest: digest(data), Schema: env.Schema, AppVersion: env.AppVersion,
			ExportedAt: ts(env.ExportedAt), SourceDevice: env.SourceDevice, Incoming: env.Summary(), Current: d.store.Summary()})
	case "importSave":
		path := a.absPath("path")
		want := a.str("digest", true, "")
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		// Re-read and re-validate, then compare with what the user previewed: the two phases share no
		// state, and a file edited in between is refused.
		data, env, err := companion.ReadSave(path)
		if d.replySaveErr(r.ID, err) {
			return
		}
		if digest(data) != want {
			d.replyErr(r.ID, "fileChanged", "the file changed since it was inspected")
			return
		}
		d.afterImport(r.ID, d.store.ApplyImport(env, d.importUsage()))
	case "listSnapshots":
		d.reply(r.ID, snapshotsReply{Snapshots: d.store.ListSnapshots()})
	case "createSnapshot":
		snap, err := d.store.CreateSnapshot()
		if d.replySaveErr(r.ID, err) {
			return
		}
		d.reply(r.ID, snapshotReply{Snapshot: snap})
	case "restoreSnapshot":
		id := a.str("id", true, "")
		if a.err == nil && !snapshotID.MatchString(id) {
			a.fail(badArgs("id must be a snapshot file name"))
		}
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		d.afterImport(r.ID, d.store.RestoreSnapshot(id, d.importUsage()))
	}
}

// importUsage is the usage an import re-seeds the device ledger from: today's, once this process
// has scanned, as the regular update uses (daemon.go updateCompanion).
func (d *daemon) importUsage() companion.UsageInput {
	if !d.scanned {
		return companion.UsageInput{}
	}
	return d.usageInput(d.cfg.Now())
}

// afterImport answers an import or restore: the new state, then the result, then a normal update
// that loads the evolution line (and through the state, the menu sprite). Draws requeued for the
// old egg are dropped so they cannot roll the imported one.
func (d *daemon) afterImport(id int64, err error) {
	if d.replySaveErr(id, err) {
		return
	}
	d.rng.forget()
	d.emitState(true)
	d.reply(id, summaryReply{Summary: d.store.Summary()})
	d.warmPasses = 0
	d.companionUpdate()
}

// replySaveErr answers err with its protocol code; false when err is nil. A Store rule code wins
// over the file error that caused it (a backup that failed on a full disk is backupFailed).
func (d *daemon) replySaveErr(id int64, err error) bool {
	if err == nil {
		return false
	}
	d.logf("save command: %v", err)
	d.replyErr(id, saveErrCode(err), err.Error())
	return true
}

func saveErrCode(err error) string {
	var ge *companion.Error
	switch {
	case errors.As(err, &ge):
		return ge.Code
	case errors.Is(err, fs.ErrNotExist):
		return "fileNotFound"
	case errors.Is(err, fs.ErrPermission):
		return "permissionDenied"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "noSpace"
	}
	return "ioError"
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// absPath reads a required absolute path (protocol §7.7: a relative path is badArgs).
func (a *args) absPath(name string) string {
	p := a.str(name, true, "")
	if a.err == nil && !filepath.IsAbs(p) {
		a.fail(badArgs("%s must be an absolute path", name))
	}
	return p
}

// forget drops the recorded and requeued draws.
func (r *replayRng) forget() {
	r.drawn = r.drawn[:0]
	r.queued = nil
}
