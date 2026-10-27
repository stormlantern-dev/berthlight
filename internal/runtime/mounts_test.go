package runtime

import "testing"

func TestParseBindMounts(t *testing.T) {
	out := "/shop\t" + `[{"Type":"bind","Source":"/home/u/webshop","Destination":"/workspaces/webshop"},{"Type":"volume","Source":"/var/lib/v","Destination":"/cache"}]` +
		"\t" + `{"bl-webshop":{}}` + "\nproxy\t[]\t{}\n"
	got, err := parseBindMounts(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Container != "shop" || got[0].Source != "/home/u/webshop" || got[0].Destination != "/workspaces/webshop" || len(got[0].Networks) != 1 || got[0].Networks[0] != "bl-webshop" {
		t.Errorf("got %+v", got)
	}
}
