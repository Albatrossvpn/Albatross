// Country names and the server-name reader, mirrored from internal/geo (Go);
// keep the two in step.
export const COUNTRIES = {
  AD: "Andorra", AE: "United Arab Emirates", AF: "Afghanistan", AG: "Antigua and Barbuda", AL: "Albania", AM: "Armenia", AO: "Angola", AR: "Argentina", AT: "Austria", AU: "Australia", AZ: "Azerbaijan", BA: "Bosnia and Herzegovina", BB: "Barbados", BD: "Bangladesh", BE: "Belgium", BF: "Burkina Faso", BG: "Bulgaria", BH: "Bahrain", BJ: "Benin", BN: "Brunei", BO: "Bolivia", BR: "Brazil", BS: "Bahamas", BT: "Bhutan", BW: "Botswana", BY: "Belarus", BZ: "Belize", CA: "Canada", CD: "DR Congo", CH: "Switzerland", CI: "Ivory Coast", CL: "Chile", CM: "Cameroon", CN: "China", CO: "Colombia", CR: "Costa Rica", CU: "Cuba", CV: "Cape Verde", CY: "Cyprus", CZ: "Czechia", DE: "Germany", DJ: "Djibouti", DK: "Denmark", DO: "Dominican Republic", DZ: "Algeria", EC: "Ecuador", EE: "Estonia", EG: "Egypt", ES: "Spain", ET: "Ethiopia", FI: "Finland", FJ: "Fiji", FR: "France", GA: "Gabon", GB: "United Kingdom", GE: "Georgia", GH: "Ghana", GI: "Gibraltar", GL: "Greenland", GR: "Greece", GT: "Guatemala", GU: "Guam", HK: "Hong Kong", HN: "Honduras", HR: "Croatia", HT: "Haiti", HU: "Hungary", ID: "Indonesia", IE: "Ireland", IL: "Israel", IM: "Isle of Man", IN: "India", IQ: "Iraq", IR: "Iran", IS: "Iceland", IT: "Italy", JE: "Jersey", JM: "Jamaica", JO: "Jordan", JP: "Japan", KE: "Kenya", KG: "Kyrgyzstan", KH: "Cambodia", KR: "South Korea", KW: "Kuwait", KY: "Cayman Islands", KZ: "Kazakhstan", LA: "Laos", LB: "Lebanon", LI: "Liechtenstein", LK: "Sri Lanka", LT: "Lithuania", LU: "Luxembourg", LV: "Latvia", LY: "Libya", MA: "Morocco", MC: "Monaco", MD: "Moldova", ME: "Montenegro", MG: "Madagascar", MK: "North Macedonia", ML: "Mali", MM: "Myanmar", MN: "Mongolia", MO: "Macao", MT: "Malta", MU: "Mauritius", MV: "Maldives", MX: "Mexico", MY: "Malaysia", MZ: "Mozambique", NA: "Namibia", NC: "New Caledonia", NG: "Nigeria", NI: "Nicaragua", NL: "Netherlands", NO: "Norway", NP: "Nepal", NZ: "New Zealand", OM: "Oman", PA: "Panama", PE: "Peru", PG: "Papua New Guinea", PH: "Philippines", PK: "Pakistan", PL: "Poland", PR: "Puerto Rico", PS: "Palestine", PT: "Portugal", PY: "Paraguay", QA: "Qatar", RE: "Réunion", RO: "Romania", RS: "Serbia", RU: "Russia", RW: "Rwanda", SA: "Saudi Arabia", SC: "Seychelles", SD: "Sudan", SE: "Sweden", SG: "Singapore", SI: "Slovenia", SK: "Slovakia", SN: "Senegal", SO: "Somalia", SV: "El Salvador", SY: "Syria", TG: "Togo", TH: "Thailand", TJ: "Tajikistan", TM: "Turkmenistan", TN: "Tunisia", TR: "Turkey", TT: "Trinidad and Tobago", TW: "Taiwan", TZ: "Tanzania", UA: "Ukraine", UG: "Uganda", US: "United States", UY: "Uruguay", UZ: "Uzbekistan", VE: "Venezuela", VG: "British Virgin Islands", VN: "Vietnam", YE: "Yemen", ZA: "South Africa", ZM: "Zambia", ZW: "Zimbabwe"
};
const ALIASES = {"usa": "US", "america": "US", "united states of america": "US", "uk": "GB", "england": "GB", "britain": "GB", "great britain": "GB", "deutschland": "DE", "holland": "NL", "the netherlands": "NL", "korea": "KR", "south-korea": "KR", "uae": "AE", "emirates": "AE", "dubai": "AE", "russian federation": "RU", "türkiye": "TR", "turkiye": "TR", "czech republic": "CZ", "hongkong": "HK", "hong-kong": "HK", "viet nam": "VN", "frankfurt": "DE", "amsterdam": "NL", "london": "GB", "paris": "FR", "tokyo": "JP", "istanbul": "TR", "helsinki": "FI", "stockholm": "SE", "warsaw": "PL", "moscow": "RU", "toronto": "CA", "singapur": "SG", "los angeles": "US", "new york": "US", "seattle": "US", "chicago": "US", "dallas": "US", "miami": "US", "san jose": "US", "falkenstein": "DE", "nuremberg": "DE", "mumbai": "IN", "seoul": "KR", "osaka": "JP", "sydney": "AU", "zurich": "CH", "vienna": "AT", "milan": "IT", "madrid": "ES", "bucharest": "RO", "kyiv": "UA", "riga": "LV", "tallinn": "EE", "vilnius": "LT", "oslo": "NO", "copenhagen": "DK", "dublin": "IE", "brussels": "BE", "prague": "CZ", "budapest": "HU", "sofia": "BG", "tehran": "IR", "yerevan": "AM", "tbilisi": "GE", "baku": "AZ", "almaty": "KZ", "taipei": "TW"};
const STOP = new Set(["VL", "VM", "SS", "WS", "XX", "NA", "OK", "TG", "ID", "IP", "HY", "TU", "RE", "DO", "ME", "IN", "IT", "TO", "BY", "IS", "AT", "AS", "OR", "NO", "AM", "PM", "GO", "HI", "SO", "UP", "MY"]);

const NAMES = new Map(Object.entries(COUNTRIES).map(([c, n]) => [n.toLowerCase(), c]));
for (const [a, c] of Object.entries(ALIASES)) NAMES.set(a, c);

/** The country a server's publisher named it after: a flag, a name or a code; "" when none. */
export function hint(name) {
  if (!name) return "";
  const f = /[\u{1F1E6}-\u{1F1FF}]{2}/u.exec(name);
  if (f) {
    const code = String.fromCharCode(...Array.from(f[0], (ch) => ch.codePointAt(0) - 0x1f1e6 + 65));
    return code === "UK" ? "GB" : code;
  }
  const words = name.toLowerCase().split(/[^\p{L}\u200c]+/u).filter(Boolean);
  for (let size = 3; size >= 1; size--) {
    for (let i = 0; i + size <= words.length; i++) {
      const c = NAMES.get(words.slice(i, i + size).join(" "));
      if (c) return c;
    }
  }
  for (const m of name.matchAll(/(?:^|[^A-Za-z])([A-Z]{2})(?=[^A-Za-z]|$)/g)) {
    const c = m[1];
    if (STOP.has(c)) continue;
    if (c === "UK") return "GB";
    if (COUNTRIES[c]) return c;
  }
  return "";
}

/** The flag of a country code. */
export const flag = (code) => (/^[A-Z]{2}$/.test(code) ? String.fromCodePoint(...Array.from(code, (ch) => 0x1f1e6 + ch.charCodeAt(0) - 65)) : "");

