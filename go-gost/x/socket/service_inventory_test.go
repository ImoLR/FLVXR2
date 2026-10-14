package socket

import (
	"reflect"
	"testing"

	core "github.com/go-gost/core/service"
	"github.com/go-gost/x/registry"
)

type inventoryServiceWithoutConns struct{ core.Service }

func (*inventoryServiceWithoutConns) Close() error { return nil }

type inventoryNFT struct{ NftablesManagerInterface }

func (*inventoryNFT) ListForwardIDs() []int64 { return []int64{987654} }

func TestListServicesIncludesServicesWithoutCurrentConns(t *testing.T) {
	const name = "inventory-without-current-conns"
	if err := registry.ServiceRegistry().Register(name, &inventoryServiceWithoutConns{}); err != nil {
		t.Fatal(err)
	}
	defer registry.ServiceRegistry().Unregister(name)
	w := &WebSocketReporter{nftablesMgr: &inventoryNFT{}}
	before := registry.ServiceRegistry().GetAll()
	i, err := w.listServices()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range i.Services {
		if item == name {
			found = true
		}
	}
	if !found || !reflect.DeepEqual(i.Nftables, []string{"987654_nft"}) {
		t.Fatalf("incomplete inventory: %+v", i)
	}
	if !reflect.DeepEqual(before, registry.ServiceRegistry().GetAll()) {
		t.Fatal("inventory modified registry")
	}
	w.nftablesMgr = nil
	if _, ok := w.collectServiceConnections()[name]; ok {
		t.Fatal("test service should demonstrate old report's CurrentConns limitation")
	}
}
