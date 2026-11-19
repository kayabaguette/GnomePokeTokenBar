package daemon

// prayReply is the pray reply data (protocol §7.6).
type prayReply struct {
	Count int `json:"count"`
}

// cmdPray counts one prayer over the egg (protocol §7.6). The Store saves it; the state that shows
// the new egg.prayers goes out before the result. While a Pokémon is active it fails with notEgg.
func (d *daemon) cmdPray(r request) {
	if _, err := newArgs(r.Args); err != nil {
		d.replyErr(r.ID, "badArgs", err.Error())
		return
	}
	n, err := d.store.Pray()
	if d.replyGameErr(r.ID, err) {
		return
	}
	d.emitState(true)
	d.reply(r.ID, prayReply{Count: n})
}
