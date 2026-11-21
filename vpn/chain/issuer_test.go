package chain

import (
	"context"
	"strings"
	"testing"
)

func TestIssuerCalls(t *testing.T) {
	sent := `{"txhash":"AB","code":0}`
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"purchase":[{"blinded":["AAA="],"deadline":"90"},{"id":"1","key_id":"2","blinded":["BBB="],"deadline":"95","signatures":["CCC="]}],"pagination":{}}`),
		ok(sent), ok(sent),
		ok(sent), ok(sent),
	}}
	c := newCLI(s)
	ps, err := c.Purchases(context.Background())
	if err != nil || len(ps) != 2 || ps[0].ID != 0 || ps[0].Fulfilled || ps[0].Deadline != 90 || ps[1].KeyID != 2 || !ps[1].Fulfilled {
		t.Fatalf("%+v %v", ps, err)
	}
	if got := strings.Join(s.calls[0], " "); !strings.HasPrefix(got, "query tickets list-purchase --page-limit 10000 -o json") {
		t.Fatalf("call %s", got)
	}
	if err := c.FulfilPurchase(context.Background(), 3, []string{"QUJD", "REVG"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[1], " "); !strings.HasPrefix(got, "tx tickets fulfil-purchase 3 QUJD REVG --from") {
		t.Fatalf("call %s", got)
	}
	if err := c.FulfilPurchase(context.Background(), 3, []string{"not base64!"}); err == nil {
		t.Fatal("bad signature sent")
	}
	if err := c.BankSend(context.Background(), "smart1xyz", "5usnode"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.calls[3], " "); !strings.HasPrefix(got, "tx bank send alice smart1xyz 5usnode --from") {
		t.Fatalf("call %s", got)
	}
}
