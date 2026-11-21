package app

import (
	"context"
	"fmt"

	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

// Chain upgrades.
//
// An upgrade changes the chain's software without restarting the network:
// a governance proposal names an upgrade and a block height; at that height
// every node stops, Cosmovisor starts the new version, and the new version
// runs the upgrade's handler once, carrying all state forward.
//
// To add an upgrade:
//  1. Add an entry to Upgrades below, named after the release (v0.3.0).
//  2. If a module's stored data changes shape (a new setting, a renamed
//     field), raise that module's ConsensusVersion and register a migration
//     in its module.go: RunMigrations runs it. A new setting needs a
//     migration that fills in its default, or the module's checks refuse it.
//  3. If a module is added or removed, list its store in StoreUpgrades.
//  4. Rehearse it (launch/upgrade-rehearsal.sh) before proposing it.
//
// Upgrades are never removed from the list: a node syncing from the start
// needs every one of them.

// Upgrade is one named chain upgrade.
type Upgrade struct {
	Name string
	// Handler runs once, at the upgrade height, in the new version. Most
	// upgrades need only the module migrations, which RunMigrations does.
	Handler func(app *App) upgradetypes.UpgradeHandler
	// StoreUpgrades adds, renames or deletes module stores.
	StoreUpgrades storetypes.StoreUpgrades
}

// runMigrations is the handler for an upgrade that only needs each module's
// registered migrations.
func runMigrations(app *App) upgradetypes.UpgradeHandler {
	return func(ctx context.Context, plan upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
		return app.ModuleManager.RunMigrations(ctx, app.Configurator(), fromVM)
	}
}

// Upgrades lists every chain upgrade, oldest first.
var Upgrades = []Upgrade{
	// v0.2.0: the first upgrade, proving the mechanism on the test network.
	// No stored data changes shape.
	{Name: "v0.2.0", Handler: runMigrations},
}

// registerUpgrades installs every upgrade's handler, and, when this node is
// starting just after an upgrade height, the store changes for it.
func (app *App) registerUpgrades() error {
	seen := map[string]bool{}
	for _, u := range Upgrades {
		if seen[u.Name] {
			return fmt.Errorf("upgrade %s is listed twice", u.Name)
		}
		seen[u.Name] = true
		app.UpgradeKeeper.SetUpgradeHandler(u.Name, u.Handler(app))
	}

	plan, err := app.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		return fmt.Errorf("reading upgrade info: %w", err)
	}
	if plan.Name == "" || app.UpgradeKeeper.IsSkipHeight(plan.Height) {
		return nil
	}
	for _, u := range Upgrades {
		if u.Name == plan.Name {
			upgrades := u.StoreUpgrades
			app.SetStoreLoader(upgradetypes.UpgradeStoreLoader(plan.Height, &upgrades))
		}
	}
	return nil
}
