# Makes internal/ui/web/map.js, the land as a grid of dots and where each
# country is, for the app's globe and maps, from Natural Earth's 1:110m
# countries (public domain):
#
#   curl -LO https://cdn.jsdelivr.net/gh/nvkelso/natural-earth-vector@v5.1.2/geojson/ne_110m_admin_0_countries.geojson
#   python3 build/mkmap.py ne_110m_admin_0_countries.geojson internal/ui/web/map.js
#
import json, sys
src, out = sys.argv[1], sys.argv[2]
fc = json.load(open(src))
polys = []  # (bbox, rings)
places = {}
for f in fc["features"]:
    p = f["properties"]
    cc = p.get("ISO_A2_EH") or p.get("ISO_A2")
    if cc and cc != "-99":
        places[cc] = [round(p["LABEL_X"], 1), round(p["LABEL_Y"], 1)]
    g = f["geometry"]
    ps = g["coordinates"] if g["type"] == "MultiPolygon" else [g["coordinates"]]
    for poly in ps:
        xs = [x for x, y in poly[0]]; ys = [y for x, y in poly[0]]
        polys.append(((min(xs), min(ys), max(xs), max(ys)), poly))
def inring(x, y, ring):
    c = False; j = len(ring) - 1
    for i in range(len(ring)):
        xi, yi = ring[i]; xj, yj = ring[j]
        if (yi > y) != (yj > y) and x < (xj - xi) * (y - yi) / (yj - yi) + xi:
            c = not c
        j = i
    return c
def land(x, y):
    for (a, b, c, d), poly in polys:
        if a <= x <= c and b <= y <= d and inring(x, y, poly[0]) and not any(inring(x, y, h) for h in poly[1:]):
            return True
    return False
STEP, TOP, ROWS, COLS = 2, 80, 70, 180
rows = []
for r in range(ROWS):
    y = TOP - STEP * r - STEP / 2
    bits = "".join("1" if land(-180 + STEP * c + STEP / 2, y) else "0" for c in range(COLS))
    rows.append("%045x" % int(bits, 2))
# Small places the 110m map leaves out, where servers often are.
extra = {"SG": [103.8, 1.35], "HK": [114.2, 22.3], "LU": [6.1, 49.6], "MT": [14.4, 35.9], "BH": [50.6, 26.1], "MO": [113.5, 22.2],
         "MC": [7.4, 43.7], "LI": [9.6, 47.1], "AD": [1.5, 42.5], "SM": [12.5, 43.9], "MU": [57.6, -20.3], "SC": [55.5, -4.6],
         "BB": [-59.6, 13.2], "MV": [73.5, 4.2], "GI": [-5.35, 36.1], "IM": [-4.5, 54.2], "JE": [-2.1, 49.2], "GG": [-2.6, 49.5],
         "AW": [-70.0, 12.5], "CW": [-69.0, 12.2], "BM": [-64.8, 32.3], "KY": [-81.2, 19.3], "VG": [-64.6, 18.4], "MS": [-62.2, 16.7],
         "AG": [-61.8, 17.1], "KN": [-62.8, 17.3], "LC": [-61.0, 13.9], "VC": [-61.2, 13.2], "GD": [-61.7, 12.1], "DM": [-61.4, 15.4],
         "CV": [-23.6, 15.1], "GU": [144.8, 13.4], "RE": [55.5, -21.1]}
for k, v in extra.items(): places.setdefault(k, v)
js = ("// The world as dots and where each country is, from Natural Earth's\n"
      "// 1:110m countries (public domain), made by build/mkmap.py: a row of\n"
      "// hex digits per 2 degrees of latitude from 80N, one bit per 2 degrees\n"
      "// of longitude from 180W; places are [longitude, latitude].\n"
      "var WORLD = { step: 2, top: 80, cols: 180, rows: " + json.dumps(rows) + " };\n"
      "var PLACES = " + json.dumps(places, separators=(",", ":"), sort_keys=True) + ";\n")
open(out, "w").write(js)
print(len(js), sum(bin(int(r, 16)).count("1") for r in rows), "land dots", len(places), "places")
