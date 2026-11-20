package chain

import (
	"context"
	"strings"
	"testing"
)

func TestVerifyQueries(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"round":"7","start_height":"280","next_round_height":"320","seed":"ab"}`),
		ok(`{"node_info":{},"sync_info":{"latest_block_height":"301","catching_up":false}}`),
		ok(`{"prober":[{"id":"0","owner":"smart1a","status":"2"},{"id":"1","owner":"smart1b","status":"1"}],"pagination":{}}`),
		ok(`{"round":"7","selected":true,"prober_ids":["0","2","1"]}`),
		ok(`{"round":"7"}`),
	}}
	c := newCLI(s)
	r, err := c.Round(context.Background())
	if err != nil || r.Round != 7 || r.StartHeight != 280 || r.NextHeight != 320 {
		t.Fatalf("%+v %v", r, err)
	}
	h, err := c.Height(context.Background())
	if err != nil || h != 301 {
		t.Fatalf("%d %v", h, err)
	}
	p, err := c.ProberByOwner(context.Background(), "smart1b")
	if err != nil || p.ID != 1 || p.Status != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	a, err := c.Assigned(context.Background(), 4)
	if err != nil || !a.Selected || len(a.Probers) != 3 || a.Probers[1] != 2 {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = c.Assigned(context.Background(), 5)
	if err != nil || a.Selected || len(a.Probers) != 0 {
		t.Fatalf("unselected %+v %v", a, err)
	}
}

func TestSendReports(t *testing.T) {
	sent := `{"txhash":"AB","code":0}`
	s := &script{answers: []func([]string) ([]byte, error){
		ok(sent), ok(`{"txhash":"AB","code":0}`),
		ok(sent), ok(`{"txhash":"AB","code":0}`),
		ok(`{"txhash":"CD","code":5,"raw_log":"wrong round"}`),
	}}
	c := newCLI(s)
	if err := c.CommitReports(context.Background(), 7, []uint64{0, 4}, []string{"aa", "bb"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "tx verify commit-reports 7 0,4 aa bb --from alice") {
		t.Fatalf("commit call %s", got)
	}
	if err := c.RevealReport(context.Background(), 7, 4, 1, 42, "00ff"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[2], " "); !strings.HasPrefix(got, "tx verify reveal-report 7 4 1 42 00ff --from") {
		t.Fatalf("reveal call %s", got)
	}
	if err := c.RevealReport(context.Background(), 7, 4, 1, 42, "00ff"); err == nil || !strings.Contains(err.Error(), "wrong round") {
		t.Fatalf("refusal not reported: %v", err)
	}
	if err := c.CommitReports(context.Background(), 7, []uint64{1}, nil); err == nil {
		t.Fatal("mismatched lists accepted")
	}
}

func TestProberAssignmentsAndSpeeds(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"round":"9","node_ids":["0","4"]}`),
		ok(`{"round":"9"}`),
		ok(`{"params":{"round_blocks":"720","min_entry_mbps":"5","min_exit_mbps":"20"}}`),
	}}
	c := newCLI(s)
	r, ids, err := c.ProberAssignments(context.Background(), 2)
	if err != nil || r != 9 || len(ids) != 2 || ids[1] != 4 {
		t.Fatalf("%d %v %v", r, ids, err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "query verify prober-assignments 2") {
		t.Fatalf("call %s", got)
	}
	if _, ids, _ := c.ProberAssignments(context.Background(), 2); len(ids) != 0 {
		t.Fatal("empty list")
	}
	e, x, err := c.MinSpeeds(context.Background())
	if err != nil || e != 5 || x != 20 {
		t.Fatalf("%d %d %v", e, x, err)
	}
}
