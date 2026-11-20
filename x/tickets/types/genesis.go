package types

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:        DefaultParams(),
		IssuerKeyList: []IssuerKey{}, PurchaseList: []Purchase{},
		SpentList: []SpentTicket{}, ProberFund: []sdk.Coin{}, SweptKeyIds: []uint64{}}
}

// TicketKeySize is the length of a ticket public key in a spent record.
const TicketKeySize = 32

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
	if err := validateBridgeGenesis(gs); err != nil {
		return err
	}
	issuerKeyIdMap := make(map[uint64]bool)
	issuerKeyCount := gs.GetIssuerKeyCount()
	for _, elem := range gs.IssuerKeyList {
		if _, ok := issuerKeyIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for issuerKey")
		}
		if elem.Id >= issuerKeyCount {
			return fmt.Errorf("issuerKey id should be lower or equal than the last id")
		}
		issuerKeyIdMap[elem.Id] = true
	}
	purchaseIdMap := make(map[uint64]bool)
	purchaseCount := gs.GetPurchaseCount()
	for _, elem := range gs.PurchaseList {
		if _, ok := purchaseIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for purchase")
		}
		if elem.Id >= purchaseCount {
			return fmt.Errorf("purchase id should be lower or equal than the last id")
		}
		purchaseIdMap[elem.Id] = true
	}

	// Spent tickets: the right size, under a known key, and listed once.
	spentSeen := make(map[string]bool, len(gs.SpentList))
	for i, elem := range gs.SpentList {
		if len(elem.TicketKey) != TicketKeySize {
			return errorsmod.Wrapf(ErrInvalidGenesis, "spent ticket %d: key length %d, want %d", i, len(elem.TicketKey), TicketKeySize)
		}
		if !issuerKeyIdMap[elem.KeyId] {
			return errorsmod.Wrapf(ErrInvalidGenesis, "spent ticket %d: unknown issuer key %d", i, elem.KeyId)
		}
		id := fmt.Sprintf("%d/%x", elem.KeyId, elem.TicketKey)
		if spentSeen[id] {
			return errorsmod.Wrapf(ErrInvalidGenesis, "spent ticket %d: listed twice", i)
		}
		spentSeen[id] = true
	}

	// Prober fund: valid, positive, one entry per denom.
	fundSeen := make(map[string]bool, len(gs.ProberFund))
	for _, coin := range gs.ProberFund {
		if err := coin.Validate(); err != nil {
			return errorsmod.Wrap(ErrInvalidGenesis, "prober fund: "+err.Error())
		}
		if !coin.IsPositive() {
			return errorsmod.Wrap(ErrInvalidGenesis, "prober fund: amounts must be greater than zero")
		}
		if fundSeen[coin.Denom] {
			return errorsmod.Wrapf(ErrInvalidGenesis, "prober fund: denom %s listed twice", coin.Denom)
		}
		fundSeen[coin.Denom] = true
	}

	// Swept keys: known and listed once.
	sweptSeen := make(map[uint64]bool, len(gs.SweptKeyIds))
	for _, id := range gs.SweptKeyIds {
		if !issuerKeyIdMap[id] {
			return errorsmod.Wrapf(ErrInvalidGenesis, "swept key %d is not a known issuer key", id)
		}
		if sweptSeen[id] {
			return errorsmod.Wrapf(ErrInvalidGenesis, "swept key %d listed twice", id)
		}
		sweptSeen[id] = true
	}

	return gs.Params.Validate()
}

// validateBridgeGenesis checks bridge requests: unique ids below the count,
// unique keys, a known state, and a payment.
func validateBridgeGenesis(gs GenesisState) error {
	ids := map[uint64]bool{}
	keys := map[string]bool{}
	for _, r := range gs.BridgeRequests {
		if ids[r.Id] || r.Id >= gs.BridgeRequestCount {
			return fmt.Errorf("bridge request %d: duplicated or above the count", r.Id)
		}
		if keys[r.RequestKey] || r.RequestKey == "" {
			return fmt.Errorf("bridge request %d: missing or duplicated key", r.Id)
		}
		if r.State < BridgeOpen || r.State > BridgeLapsed {
			return fmt.Errorf("bridge request %d: unknown state %d", r.Id, r.State)
		}
		if err := r.Amount.Validate(); err != nil {
			return fmt.Errorf("bridge request %d: %w", r.Id, err)
		}
		ids[r.Id], keys[r.RequestKey] = true, true
	}
	return nil
}
