package chain

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mspvantage/smartnodes/vpn/node"
)

type script struct {
	calls   [][]string
	answers []func(args []string) ([]byte, error)
}

func (s *script) run(_ context.Context, args []string) ([]byte, error) {
	s.calls = append(s.calls, args)
	if len(s.answers) == 0 {
		return nil, errors.New("unexpected call: " + strings.Join(args, " "))
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a(args)
}

func ok(text string) func([]string) ([]byte, error) {
	return func([]string) ([]byte, error) { return []byte(text), nil }
}

func fail(text string) func([]string) ([]byte, error) {
	return func([]string) ([]byte, error) { return nil, errors.New(text) }
}

func newCLI(s *script) *CLI {
	return &CLI{Run: s.run, RPC: "tcp://localhost:26657", ChainID: "smartnodes", NodeID: 4,
		From: "alice", KeyringBackend: "test", Home: "/h", GasPrices: "0.025usnode",
		PollInterval: time.Millisecond, PollTimeout: 50 * time.Millisecond}
}

func TestIssuerKey(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"issuer_key":{"id":"2","pubkey":"AAEC","redeem_end":"1900000000"}}`),
	}}
	k, err := newCLI(s).IssuerKey(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Pubkey) != 3 || k.RedeemEnd != 1_900_000_000 {
		t.Fatalf("%+v", k)
	}
	want := "query tickets get-issuer-key 2 -o json --node tcp://localhost:26657"
	if got := strings.Join(s.calls[0], " "); got != want {
		t.Fatalf("command %q", got)
	}
}

func TestTicketSpent(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){ok(`{"spent":true}`), ok(`{}`)}}
	c := newCLI(s)
	key := make([]byte, 32)
	if spent, err := c.TicketSpent(context.Background(), 1, key); err != nil || !spent {
		t.Fatalf("%v %v", spent, err)
	}
	// The chain leaves out false values.
	if spent, err := c.TicketSpent(context.Background(), 1, key); err != nil || spent {
		t.Fatalf("%v %v", spent, err)
	}
	if got := s.calls[0][3]; got != "1" || s.calls[0][4] != strings.Repeat("00", 32) {
		t.Fatalf("command %v", s.calls[0])
	}
}

func TestNode(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"node":{"operator":"smart1x","endpoint":"10.0.0.1:51820","pubkey":"k","tier":"1","status":"1"}}`),
	}}
	n, err := newCLI(s).Node(context.Background(), 0)
	if err != nil || n.Endpoint != "10.0.0.1:51820" || n.Status != 1 || n.Tier != 1 {
		t.Fatalf("%+v %v", n, err)
	}
}

func TestRedeem(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok("gas estimate: 123\n{\"height\":\"0\",\"txhash\":\"ABC\",\"code\":0}"),
		fail("tx (ABC) not found"),
		ok(`{"height":"9","txhash":"ABC","code":0}`),
	}}
	if err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}, {2}}); err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(s.calls[0], " ")
	for _, want := range []string{"tx tickets redeem-tickets 4 2 AQ== Ag==", "--from alice", "--gas auto", "--yes"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command %q lacks %q", cmd, want)
		}
	}
	if strings.Join(s.calls[2][:3], " ") != "query tx ABC" {
		t.Fatalf("poll %v", s.calls[2])
	}
}

func TestRedeemNamesTheRefusedTicket(t *testing.T) {
	// What the chain prints when simulation finds a spent ticket.
	text := "exit status 1: Error: rpc error: code = Unknown desc = failed to execute message; message index: 0: ticket 1: ticket has already been redeemed [/x/tickets/keeper/msg_server.go:80] with gas used: '9'"
	s := &script{answers: []func([]string) ([]byte, error){fail(text)}}
	err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}, {2}})
	var re *node.RedeemError
	if !errors.As(err, &re) || re.Index != 1 || re.Reason != "ticket has already been redeemed" {
		t.Fatalf("%v", err)
	}
}

func TestRedeemFailedInBlock(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){
		ok(`{"txhash":"ABC","code":0}`),
		ok(`{"txhash":"ABC","code":1113,"raw_log":"failed to execute message; message index: 0: ticket 0: ticket has already been redeemed"}`),
	}}
	var re *node.RedeemError
	if err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}}); !errors.As(err, &re) || re.Index != 0 {
		t.Fatalf("%v", err)
	}
}

func TestRedeemExpiredKey(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){fail("key 2: issuer key has passed its redemption deadline")}}
	if err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}}); !errors.Is(err, node.ErrKeyExpired) {
		t.Fatalf("%v", err)
	}
}

func TestRedeemNeverIncluded(t *testing.T) {
	var answers []func([]string) ([]byte, error)
	answers = append(answers, ok(`{"txhash":"ABC","code":0}`))
	for i := 0; i < 1000; i++ {
		answers = append(answers, fail("tx (ABC) not found"))
	}
	s := &script{answers: answers}
	err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}})
	if !errors.Is(err, node.ErrUnconfirmed) {
		t.Fatalf("a sent but unconfirmed transaction must say so: %v", err)
	}
}

func TestRedeemUnreachable(t *testing.T) {
	s := &script{answers: []func([]string) ([]byte, error){fail("connection refused")}}
	err := newCLI(s).Redeem(context.Background(), 2, [][]byte{{1}})
	var re *node.RedeemError
	if err == nil || errors.As(err, &re) || errors.Is(err, node.ErrKeyExpired) || errors.Is(err, node.ErrUnconfirmed) {
		t.Fatalf("%v", err)
	}
}

func TestListNodes(t *testing.T) {
	var page strings.Builder
	page.WriteString(`{"node":[`)
	for i := 0; i < listPage; i++ {
		if i > 0 {
			page.WriteString(",")
		}
		page.WriteString(`{"id":"` + strconv.Itoa(i) + `","endpoint":"10.0.0.1:1","tier":"2","status":"1"}`)
	}
	page.WriteString(`],"pagination":{}}`)
	s := &script{answers: []func([]string) ([]byte, error){
		ok(page.String()),
		ok(`{"node":[{"endpoint":"10.0.0.9:51821","tier":"1","status":"1"}],"pagination":{}}`),
	}}
	nodes, err := newCLI(s).ListNodes(context.Background())
	if err != nil || len(nodes) != listPage+1 {
		t.Fatalf("%d nodes, %v", len(nodes), err)
	}
	if nodes[5].ID != 5 || nodes[5].Tier != TierExit || nodes[listPage].ID != 0 || nodes[listPage].Tier != TierEntry {
		t.Fatalf("%+v %+v", nodes[5], nodes[listPage])
	}
	if got := strings.Join(s.calls[1], " "); !strings.Contains(got, "registry list-node --page-offset 1000 --page-limit 1000 -o json") {
		t.Fatalf("second call %s", got)
	}
}
