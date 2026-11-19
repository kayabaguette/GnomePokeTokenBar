package daemon

import (
	"slices"

	"poketokenbar/helper/internal/companion"
	"poketokenbar/helper/internal/usage"
)

var recapScopes = []string{string(usage.ScopeWeek), string(usage.ScopeMonth), string(usage.ScopeYear)}

// maxRecapGraduates is how many graduates the recap strip lists (UsageRecapView.swift:310-346).
const maxRecapGraduates = 4

// recapView is the data of a getRecap result (protocol §7.5): the usage recap, the offset that was
// asked for and the graduates of the period.
type recapView struct {
	usage.Recap
	Offset    int                  `json:"offset"`
	Graduated int                  `json:"graduated"`
	Graduates []companion.Graduate `json:"graduates"`
}

// cmdRecap serves getRecap from local data: the ledger overlaid with the live series, and the dex.
func (d *daemon) cmdRecap(r request) {
	a, err := newArgs(r.Args)
	if err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	scope := a.str("scope", true, "")
	if a.err == nil && !slices.Contains(recapScopes, scope) {
		a.fail(badArgs("scope must be one of %v", recapScopes))
	}
	offset := int(a.int("offset", true, 0, -1000, 0))
	if a.err != nil {
		d.replyErr(r.ID, "badArgs", a.err.Error())
		return
	}
	now := d.cfg.Now().In(d.loc)
	p := usage.NewRecapPeriod(usage.RecapScope(scope), now, d.settings.weekday()).Shifted(offset)
	h := usage.NewHistory(d.ledger, d.combinedMonthDaily(now))
	v := recapView{Recap: usage.MakeRecap(h, p, now), Offset: offset}
	v.Graduated, v.Graduates = d.store.Graduates(p.Start, p.End, maxRecapGraduates)
	d.reply(r.ID, v)
}
