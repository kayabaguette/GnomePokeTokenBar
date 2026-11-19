package daemon

import (
	"errors"
	"math"

	"poketokenbar/helper/internal/companion"
)

// Shop and Bag commands (protocol §7.4). They run on the owner like every command, so the mutating
// ones apply in arrival order. A mutation is saved by the Store before the reply; the state that
// reflects it, and any evolve or graduate event, are queued before the result.

// candyPlanReply is the planCandies reply data.
type candyPlanReply struct {
	Plan *companion.CandyPlan `json:"plan"`
}

// mintReply is the useMint reply data.
type mintReply struct {
	Nature string `json:"nature"`
}

var eggTiers = []string{"uncommon", "rare"}

// shopCommand serves buyItem, buyEgg, planCandies, useCandy and useMint.
func (d *daemon) shopCommand(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	switch r.Cmd {
	case "buyItem":
		item := a.str("item", true, "")
		count := a.int("count", false, 1, math.MinInt64, math.MaxInt64)
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		p, err := d.store.Buy(item, int(count))
		if d.replyGameErr(r.ID, err) {
			return
		}
		d.emitState(true)
		d.reply(r.ID, p)
	case "buyEgg":
		var tier *string
		if _, ok := a.get("tier", true); ok {
			tier = a.nullableEnum("tier", eggTiers...)
		}
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		t := ""
		if tier != nil {
			t = *tier
		}
		p, err := d.store.BuyEgg(t)
		if d.replyGameErr(r.ID, err) {
			return
		}
		d.emitState(true)
		d.reply(r.ID, p)
		d.warmPasses = 0
		d.companionUpdate() // the species prefetch for the new egg, in the background
	case "planCandies":
		count := a.int("count", true, 0, math.MinInt64, math.MaxInt64)
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		d.reply(r.ID, candyPlanReply{Plan: d.store.PlanCandies(int(count))})
	case "useCandy":
		count := a.int("count", true, 0, 1, math.MaxInt64)
		if a.err != nil {
			d.replyErr(r.ID, "badArgs", a.err.Error())
			return
		}
		u, events, err := d.store.UseCandy(int(count))
		if d.replyGameErr(r.ID, err) {
			return
		}
		d.emitState(true)
		d.sendEvents(companionEvents(events))
		d.reply(r.ID, u)
		d.warmPasses = 0
		d.companionUpdate() // details and names of an evolved species, or the next egg's prefetch
	case "useMint":
		nature, err := d.store.UseMint()
		d.rng.commit() // the Mint's draw is never replayed
		if d.replyGameErr(r.ID, err) {
			return
		}
		d.emitState(true)
		d.reply(r.ID, mintReply{Nature: nature})
	}
}

// replyGameErr answers a Store rule violation with its protocol code; false when err is nil.
func (d *daemon) replyGameErr(id int64, err error) bool {
	if err == nil {
		return false
	}
	code := "internal" // the Store only returns *companion.Error
	var ge *companion.Error
	if errors.As(err, &ge) {
		code = ge.Code
	}
	d.replyErr(id, code, err.Error())
	return true
}
