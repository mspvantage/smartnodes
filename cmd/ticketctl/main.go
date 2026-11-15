// ticketctl is a test tool for the SmartNodes ticket protocol. It plays the
// two off-chain roles that no wallet or service fills yet:
//
//   - the buyer, who creates tickets, blinds them, unblinds the issuer's
//     signatures and hands spendable tickets to a node;
//   - the issuer, who holds the signing key and signs blinded tickets.
//
// It exists so the tickets module can be exercised on a local chain. It is
// NOT a wallet: it keeps secrets in plain JSON files and has had no security
// review. Never use it with keys or funds that matter.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	ticketcrypto "github.com/mspvantage/smartnodes/x/tickets/crypto"
)

const usage = `ticketctl - test tool for the SmartNodes ticket protocol (not a wallet)

Issuer:
  ticketctl issuer-new   -out issuer.json
  ticketctl issuer-sign  -issuer issuer.json BLINDED...

Buyer:
  ticketctl wallet-new     -out wallet.json -chain-id ID -key-id N -count K
  ticketctl wallet-blinded -wallet wallet.json
  ticketctl wallet-unblind -wallet wallet.json -pubkey PUBKEY SIGNATURE...
  ticketctl wallet-spend   -wallet wallet.json -node-id N -index I [-count C]

Values are printed space-separated in base64, ready to paste after a
smartnodesd command.
`

// issuerFile is the issuer's key pair on disk.
type issuerFile struct {
	SecretKey string `json:"secret_key"`
	PublicKey string `json:"public_key"`
}

// walletTicket is one ticket's secrets on disk.
type walletTicket struct {
	PrivateKey     string `json:"private_key"`
	BlindingFactor string `json:"blinding_factor"`
	Blinded        string `json:"blinded"`
	// Signature is the unblinded issuer signature, set by wallet-unblind.
	Signature string `json:"signature,omitempty"`
}

// walletFile is a buyer's batch of tickets for one chain and issuer key.
type walletFile struct {
	ChainID string         `json:"chain_id"`
	KeyID   uint64         `json:"key_id"`
	Tickets []walletTicket `json:"tickets"`
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func unb64(what, s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64", what)
	}
	return b, nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// writeJSON writes a secrets file readable only by its owner.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// secret rebuilds the crypto package's ticket secret from its stored form.
func (t walletTicket) secret() (*ticketcrypto.TicketSecret, error) {
	priv, err := unb64("private key", t.PrivateKey)
	if err != nil {
		return nil, err
	}
	factor, err := unb64("blinding factor", t.BlindingFactor)
	if err != nil {
		return nil, err
	}
	blinded, err := unb64("blinded value", t.Blinded)
	if err != nil {
		return nil, err
	}
	return &ticketcrypto.TicketSecret{PrivateKey: priv, BlindingFactor: factor, Blinded: blinded}, nil
}

func issuerNew(args []string) error {
	fs := flag.NewFlagSet("issuer-new", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the issuer key pair to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite a key", *out)
	}

	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	sk, pk, err := ticketcrypto.GenerateIssuerKey(seed)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, issuerFile{SecretKey: b64(sk), PublicKey: b64(pk)}); err != nil {
		return err
	}
	fmt.Println(b64(pk))
	return nil
}

func issuerSign(args []string) error {
	fs := flag.NewFlagSet("issuer-sign", flag.ContinueOnError)
	path := fs.String("issuer", "", "issuer key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() == 0 {
		return errors.New("-issuer and at least one blinded value are required")
	}
	var iss issuerFile
	if err := readJSON(*path, &iss); err != nil {
		return err
	}
	sk, err := unb64("issuer secret key", iss.SecretKey)
	if err != nil {
		return err
	}

	sigs := make([]string, 0, fs.NArg())
	for i, text := range fs.Args() {
		blinded, err := unb64(fmt.Sprintf("blinded value %d", i), text)
		if err != nil {
			return err
		}
		sig, err := ticketcrypto.BlindSign(sk, blinded)
		if err != nil {
			return fmt.Errorf("blinded value %d: %w", i, err)
		}
		sigs = append(sigs, b64(sig))
	}
	fmt.Println(strings.Join(sigs, " "))
	return nil
}

func walletNew(args []string) error {
	fs := flag.NewFlagSet("wallet-new", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the wallet to")
	chainID := fs.String("chain-id", "", "chain id the tickets are for")
	keyID := fs.Uint64("key-id", 0, "issuer key id the tickets are for")
	count := fs.Int("count", 1, "number of tickets")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || *chainID == "" {
		return errors.New("-out and -chain-id are required")
	}
	if *count < 1 || *count > 1000 {
		return errors.New("-count must be 1-1000")
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists; refusing to overwrite a wallet", *out)
	}

	w := walletFile{ChainID: *chainID, KeyID: *keyID}
	blinded := make([]string, 0, *count)
	for i := 0; i < *count; i++ {
		s, err := ticketcrypto.NewTicket(rand.Reader, *chainID, *keyID)
		if err != nil {
			return err
		}
		w.Tickets = append(w.Tickets, walletTicket{
			PrivateKey:     b64(s.PrivateKey),
			BlindingFactor: b64(s.BlindingFactor),
			Blinded:        b64(s.Blinded),
		})
		blinded = append(blinded, b64(s.Blinded))
	}
	if err := writeJSON(*out, w); err != nil {
		return err
	}
	fmt.Println(strings.Join(blinded, " "))
	return nil
}

func walletBlinded(args []string) error {
	fs := flag.NewFlagSet("wallet-blinded", flag.ContinueOnError)
	path := fs.String("wallet", "", "wallet file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("-wallet is required")
	}
	var w walletFile
	if err := readJSON(*path, &w); err != nil {
		return err
	}
	blinded := make([]string, 0, len(w.Tickets))
	for _, t := range w.Tickets {
		blinded = append(blinded, t.Blinded)
	}
	fmt.Println(strings.Join(blinded, " "))
	return nil
}

func walletUnblind(args []string) error {
	fs := flag.NewFlagSet("wallet-unblind", flag.ContinueOnError)
	path := fs.String("wallet", "", "wallet file")
	pubkey := fs.String("pubkey", "", "issuer public key, base64")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *pubkey == "" {
		return errors.New("-wallet and -pubkey are required")
	}
	var w walletFile
	if err := readJSON(*path, &w); err != nil {
		return err
	}
	if fs.NArg() != len(w.Tickets) {
		return fmt.Errorf("got %d signatures for %d tickets", fs.NArg(), len(w.Tickets))
	}
	pk, err := unb64("issuer public key", *pubkey)
	if err != nil {
		return err
	}

	for i, text := range fs.Args() {
		blindSig, err := unb64(fmt.Sprintf("signature %d", i), text)
		if err != nil {
			return err
		}
		secret, err := w.Tickets[i].secret()
		if err != nil {
			return fmt.Errorf("ticket %d: %w", i, err)
		}
		// Unblind also checks the result against the issuer public key, so a
		// bad signature is caught here and not when a node tries to redeem.
		sig, err := ticketcrypto.Unblind(secret, pk, w.ChainID, w.KeyID, blindSig)
		if err != nil {
			return fmt.Errorf("ticket %d: %w", i, err)
		}
		w.Tickets[i].Signature = b64(sig)
	}
	if err := writeJSON(*path, w); err != nil {
		return err
	}
	fmt.Printf("unblinded and verified %d tickets\n", len(w.Tickets))
	return nil
}

func walletSpend(args []string) error {
	fs := flag.NewFlagSet("wallet-spend", flag.ContinueOnError)
	path := fs.String("wallet", "", "wallet file")
	nodeID := fs.Uint64("node-id", 0, "id of the node being paid")
	index := fs.Int("index", 0, "first ticket to spend, counting from 0")
	count := fs.Int("count", 1, "number of tickets to spend")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("-wallet is required")
	}
	var w walletFile
	if err := readJSON(*path, &w); err != nil {
		return err
	}
	if *index < 0 || *count < 1 || *index+*count > len(w.Tickets) {
		return fmt.Errorf("tickets %d-%d are out of range; the wallet holds %d", *index, *index+*count-1, len(w.Tickets))
	}

	tickets := make([]string, 0, *count)
	for i := *index; i < *index+*count; i++ {
		t := w.Tickets[i]
		if t.Signature == "" {
			return fmt.Errorf("ticket %d has no signature yet; run wallet-unblind first", i)
		}
		secret, err := t.secret()
		if err != nil {
			return fmt.Errorf("ticket %d: %w", i, err)
		}
		sig, err := unb64("issuer signature", t.Signature)
		if err != nil {
			return err
		}
		ticket, err := ticketcrypto.Spend(secret, sig, w.ChainID, w.KeyID, *nodeID)
		if err != nil {
			return fmt.Errorf("ticket %d: %w", i, err)
		}
		tickets = append(tickets, b64(ticket))
	}
	fmt.Println(strings.Join(tickets, " "))
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	commands := map[string]func([]string) error{
		"issuer-new":     issuerNew,
		"issuer-sign":    issuerSign,
		"wallet-new":     walletNew,
		"wallet-blinded": walletBlinded,
		"wallet-unblind": walletUnblind,
		"wallet-spend":   walletSpend,
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
