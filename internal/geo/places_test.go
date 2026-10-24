package geo

import "testing"

func TestPlace(t *testing.T) {
	if lat, lon, ok := Place("de"); !ok || lat < 47 || lat > 55 || lon < 5 || lon > 16 {
		t.Fatalf("Place(de) = %v, %v, %v", lat, lon, ok)
	}
	if _, _, ok := Place("XX"); ok {
		t.Fatal("Place(XX) found a place")
	}
	if len(byPlace) < 150 {
		t.Fatalf("only %d places", len(byPlace))
	}
	// Every place is a point on the map in a country the app knows.
	for code, p := range byPlace {
		if _, ok := Lookup(code); !ok {
			t.Errorf("%s is not a known country", code)
		}
		if p == [2]float64{} || p[0] < -90 || p[0] > 90 || p[1] < -180 || p[1] > 180 {
			t.Errorf("%s: %v is not a point", code, p)
		}
	}
}
