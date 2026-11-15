package types

import "fmt"

// DefaultGenesis returns the default genesis state
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		Params:        DefaultParams(),
		IssuerKeyList: []IssuerKey{}, PurchaseList: []Purchase{}}
}

// Validate performs basic genesis state validation returning an error upon any
// failure.
func (gs GenesisState) Validate() error {
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

	return gs.Params.Validate()
}
