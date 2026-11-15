package types

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
)

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:         DefaultParams(),
		ProberList:     []Prober{},
		NodeRecordList: []NodeRecord{},
	}
}

// ValidateBasic checks a stored prober for internal consistency. It is used
// when importing genesis.
func (p Prober) ValidateBasic() error {
	if p.Owner == "" {
		return errorsmod.Wrap(ErrInvalidProber, "empty owner")
	}
	switch p.Status {
	case ProberPending, ProberActive:
		if p.UnbondingEnd != 0 {
			return errorsmod.Wrap(ErrInvalidProber, "prober that has not left has an unbonding end time")
		}
	case ProberLeaving, ProberUnbonding:
		if p.UnbondingEnd <= 0 {
			return errorsmod.Wrap(ErrInvalidProber, "leaving prober has no unbonding end time")
		}
	default:
		return errorsmod.Wrapf(ErrInvalidProber, "unknown status %d", p.Status)
	}
	return nil
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
	proberIdMap := make(map[uint64]bool)
	ownerMap := make(map[string]bool)
	proberCount := gs.GetProberCount()
	for _, elem := range gs.ProberList {
		if _, ok := proberIdMap[elem.Id]; ok {
			return fmt.Errorf("duplicated id for prober")
		}
		if elem.Id >= proberCount {
			return fmt.Errorf("prober id should be lower or equal than the last id")
		}
		proberIdMap[elem.Id] = true

		if err := elem.ValidateBasic(); err != nil {
			return errorsmod.Wrapf(err, "prober %d", elem.Id)
		}
		if ownerMap[elem.Owner] {
			return fmt.Errorf("owner %s has more than one prober", elem.Owner)
		}
		ownerMap[elem.Owner] = true
	}

	nodeMap := make(map[uint64]bool)
	for _, rec := range gs.NodeRecordList {
		if nodeMap[rec.NodeId] {
			return fmt.Errorf("duplicated node record for node %d", rec.NodeId)
		}
		nodeMap[rec.NodeId] = true
	}

	return gs.Params.Validate()
}
