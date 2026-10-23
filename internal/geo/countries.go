// Package geo knows country names and guesses a server's country from the
// name its publisher gave it.
package geo

import "strings"

// Country is an ISO 3166-1 alpha-2 country.
type Country struct {
	Code string `json:"code"`
	EN   string `json:"en"`
}

// countries is "CODE|English name", one per line.
const countries = `AD|Andorra
AE|United Arab Emirates
AF|Afghanistan
AG|Antigua and Barbuda
AL|Albania
AM|Armenia
AO|Angola
AR|Argentina
AT|Austria
AU|Australia
AZ|Azerbaijan
BA|Bosnia and Herzegovina
BB|Barbados
BD|Bangladesh
BE|Belgium
BF|Burkina Faso
BG|Bulgaria
BH|Bahrain
BJ|Benin
BN|Brunei
BO|Bolivia
BR|Brazil
BS|Bahamas
BT|Bhutan
BW|Botswana
BY|Belarus
BZ|Belize
CA|Canada
CD|DR Congo
CH|Switzerland
CI|Ivory Coast
CL|Chile
CM|Cameroon
CN|China
CO|Colombia
CR|Costa Rica
CU|Cuba
CV|Cape Verde
CY|Cyprus
CZ|Czechia
DE|Germany
DJ|Djibouti
DK|Denmark
DO|Dominican Republic
DZ|Algeria
EC|Ecuador
EE|Estonia
EG|Egypt
ES|Spain
ET|Ethiopia
FI|Finland
FJ|Fiji
FR|France
GA|Gabon
GB|United Kingdom
GE|Georgia
GH|Ghana
GI|Gibraltar
GL|Greenland
GR|Greece
GT|Guatemala
GU|Guam
HK|Hong Kong
HN|Honduras
HR|Croatia
HT|Haiti
HU|Hungary
ID|Indonesia
IE|Ireland
IL|Israel
IM|Isle of Man
IN|India
IQ|Iraq
IR|Iran
IS|Iceland
IT|Italy
JE|Jersey
JM|Jamaica
JO|Jordan
JP|Japan
KE|Kenya
KG|Kyrgyzstan
KH|Cambodia
KR|South Korea
KW|Kuwait
KY|Cayman Islands
KZ|Kazakhstan
LA|Laos
LB|Lebanon
LI|Liechtenstein
LK|Sri Lanka
LT|Lithuania
LU|Luxembourg
LV|Latvia
LY|Libya
MA|Morocco
MC|Monaco
MD|Moldova
ME|Montenegro
MG|Madagascar
MK|North Macedonia
ML|Mali
MM|Myanmar
MN|Mongolia
MO|Macao
MT|Malta
MU|Mauritius
MV|Maldives
MX|Mexico
MY|Malaysia
MZ|Mozambique
NA|Namibia
NC|New Caledonia
NG|Nigeria
NI|Nicaragua
NL|Netherlands
NO|Norway
NP|Nepal
NZ|New Zealand
OM|Oman
PA|Panama
PE|Peru
PG|Papua New Guinea
PH|Philippines
PK|Pakistan
PL|Poland
PR|Puerto Rico
PS|Palestine
PT|Portugal
PY|Paraguay
QA|Qatar
RE|Réunion
RO|Romania
RS|Serbia
RU|Russia
RW|Rwanda
SA|Saudi Arabia
SC|Seychelles
SD|Sudan
SE|Sweden
SG|Singapore
SI|Slovenia
SK|Slovakia
SN|Senegal
SO|Somalia
SV|El Salvador
SY|Syria
TG|Togo
TH|Thailand
TJ|Tajikistan
TM|Turkmenistan
TN|Tunisia
TR|Turkey
TT|Trinidad and Tobago
TW|Taiwan
TZ|Tanzania
UA|Ukraine
UG|Uganda
US|United States
UY|Uruguay
UZ|Uzbekistan
VE|Venezuela
VG|British Virgin Islands
VN|Vietnam
YE|Yemen
ZA|South Africa
ZM|Zambia
ZW|Zimbabwe`

var (
	byCode = map[string]Country{}
	// nameToCode maps lower-case names and common aliases to codes.
	nameToCode = map[string]string{}
)

func init() {
	for _, line := range strings.Split(countries, "\n") {
		p := strings.Split(line, "|")
		c := Country{Code: p[0], EN: p[1]}
		byCode[c.Code] = c
		nameToCode[strings.ToLower(c.EN)] = c.Code
	}
	for alias, code := range map[string]string{
		"usa": "US", "america": "US", "united states of america": "US", "uk": "GB", "england": "GB", "britain": "GB",
		"great britain": "GB", "deutschland": "DE", "holland": "NL", "the netherlands": "NL", "korea": "KR",
		"south-korea": "KR", "uae": "AE", "emirates": "AE", "dubai": "AE", "russian federation": "RU",
		"türkiye": "TR", "turkiye": "TR", "czech republic": "CZ", "hongkong": "HK", "hong-kong": "HK",
		"viet nam": "VN", "frankfurt": "DE", "amsterdam": "NL", "london": "GB", "paris": "FR", "tokyo": "JP",
		"istanbul": "TR", "helsinki": "FI", "stockholm": "SE", "warsaw": "PL", "moscow": "RU", "toronto": "CA",
		"singapur": "SG", "los angeles": "US", "new york": "US", "seattle": "US", "chicago": "US", "dallas": "US",
		"miami": "US", "san jose": "US", "falkenstein": "DE", "nuremberg": "DE", "mumbai": "IN", "seoul": "KR",
		"osaka": "JP", "sydney": "AU", "zurich": "CH", "vienna": "AT", "milan": "IT", "madrid": "ES",
		"bucharest": "RO", "kyiv": "UA", "riga": "LV", "tallinn": "EE", "vilnius": "LT", "oslo": "NO",
		"copenhagen": "DK", "dublin": "IE", "brussels": "BE", "prague": "CZ", "budapest": "HU", "sofia": "BG",
		"tehran": "IR", "yerevan": "AM", "tbilisi": "GE", "baku": "AZ", "almaty": "KZ", "taipei": "TW",
	} {
		nameToCode[alias] = code
	}
}

// Lookup returns the country for an ISO code.
func Lookup(code string) (Country, bool) {
	c, ok := byCode[strings.ToUpper(code)]
	if !ok && len(code) == 2 {
		up := strings.ToUpper(code)
		return Country{Code: up, EN: up}, false
	}
	return c, ok
}

// All returns every known country.
func All() []Country {
	out := make([]Country, 0, len(byCode))
	for _, c := range byCode {
		out = append(out, c)
	}
	return out
}

// Flag returns the emoji flag for a country code.
func Flag(code string) string {
	if len(code) != 2 {
		return "🌐"
	}
	code = strings.ToUpper(code)
	return string([]rune{rune(code[0]) - 'A' + 0x1F1E6, rune(code[1]) - 'A' + 0x1F1E6})
}
