// Package geo knows country names and guesses a server's country from the
// name its publisher gave it.
package geo

import "strings"

// Country is an ISO 3166-1 alpha-2 country.
type Country struct {
	Code string `json:"code"`
	EN   string `json:"en"`
	FA   string `json:"fa"`
}

// countries is "CODE|English name|Persian name", one per line.
const countries = `AD|Andorra|آندورا
AE|United Arab Emirates|امارات
AF|Afghanistan|افغانستان
AG|Antigua and Barbuda|آنتیگوا و باربودا
AL|Albania|آلبانی
AM|Armenia|ارمنستان
AO|Angola|آنگولا
AR|Argentina|آرژانتین
AT|Austria|اتریش
AU|Australia|استرالیا
AZ|Azerbaijan|جمهوری آذربایجان
BA|Bosnia and Herzegovina|بوسنی و هرزگوین
BB|Barbados|باربادوس
BD|Bangladesh|بنگلادش
BE|Belgium|بلژیک
BF|Burkina Faso|بورکینافاسو
BG|Bulgaria|بلغارستان
BH|Bahrain|بحرین
BJ|Benin|بنین
BN|Brunei|برونئی
BO|Bolivia|بولیوی
BR|Brazil|برزیل
BS|Bahamas|باهاما
BT|Bhutan|بوتان
BW|Botswana|بوتسوانا
BY|Belarus|بلاروس
BZ|Belize|بلیز
CA|Canada|کانادا
CD|DR Congo|کنگو
CH|Switzerland|سوئیس
CI|Ivory Coast|ساحل عاج
CL|Chile|شیلی
CM|Cameroon|کامرون
CN|China|چین
CO|Colombia|کلمبیا
CR|Costa Rica|کاستاریکا
CU|Cuba|کوبا
CV|Cape Verde|کیپ ورد
CY|Cyprus|قبرس
CZ|Czechia|چک
DE|Germany|آلمان
DJ|Djibouti|جیبوتی
DK|Denmark|دانمارک
DO|Dominican Republic|جمهوری دومینیکن
DZ|Algeria|الجزایر
EC|Ecuador|اکوادور
EE|Estonia|استونی
EG|Egypt|مصر
ES|Spain|اسپانیا
ET|Ethiopia|اتیوپی
FI|Finland|فنلاند
FJ|Fiji|فیجی
FR|France|فرانسه
GA|Gabon|گابن
GB|United Kingdom|انگلستان
GE|Georgia|گرجستان
GH|Ghana|غنا
GI|Gibraltar|جبل‌الطارق
GL|Greenland|گرینلند
GR|Greece|یونان
GT|Guatemala|گواتمالا
GU|Guam|گوام
HK|Hong Kong|هنگ کنگ
HN|Honduras|هندوراس
HR|Croatia|کرواسی
HT|Haiti|هائیتی
HU|Hungary|مجارستان
ID|Indonesia|اندونزی
IE|Ireland|ایرلند
IL|Israel|اسرائیل
IM|Isle of Man|جزیره من
IN|India|هند
IQ|Iraq|عراق
IR|Iran|ایران
IS|Iceland|ایسلند
IT|Italy|ایتالیا
JE|Jersey|جرزی
JM|Jamaica|جامائیکا
JO|Jordan|اردن
JP|Japan|ژاپن
KE|Kenya|کنیا
KG|Kyrgyzstan|قرقیزستان
KH|Cambodia|کامبوج
KR|South Korea|کره جنوبی
KW|Kuwait|کویت
KY|Cayman Islands|جزایر کیمن
KZ|Kazakhstan|قزاقستان
LA|Laos|لائوس
LB|Lebanon|لبنان
LI|Liechtenstein|لیختن‌اشتاین
LK|Sri Lanka|سریلانکا
LT|Lithuania|لیتوانی
LU|Luxembourg|لوکزامبورگ
LV|Latvia|لتونی
LY|Libya|لیبی
MA|Morocco|مراکش
MC|Monaco|موناکو
MD|Moldova|مولداوی
ME|Montenegro|مونته‌نگرو
MG|Madagascar|ماداگاسکار
MK|North Macedonia|مقدونیه شمالی
ML|Mali|مالی
MM|Myanmar|میانمار
MN|Mongolia|مغولستان
MO|Macao|ماکائو
MT|Malta|مالت
MU|Mauritius|موریس
MV|Maldives|مالدیو
MX|Mexico|مکزیک
MY|Malaysia|مالزی
MZ|Mozambique|موزامبیک
NA|Namibia|نامیبیا
NC|New Caledonia|کالدونیای جدید
NG|Nigeria|نیجریه
NI|Nicaragua|نیکاراگوئه
NL|Netherlands|هلند
NO|Norway|نروژ
NP|Nepal|نپال
NZ|New Zealand|نیوزیلند
OM|Oman|عمان
PA|Panama|پاناما
PE|Peru|پرو
PG|Papua New Guinea|پاپوآ گینه نو
PH|Philippines|فیلیپین
PK|Pakistan|پاکستان
PL|Poland|لهستان
PR|Puerto Rico|پورتوریکو
PS|Palestine|فلسطین
PT|Portugal|پرتغال
PY|Paraguay|پاراگوئه
QA|Qatar|قطر
RE|Réunion|رئونیون
RO|Romania|رومانی
RS|Serbia|صربستان
RU|Russia|روسیه
RW|Rwanda|رواندا
SA|Saudi Arabia|عربستان
SC|Seychelles|سیشل
SD|Sudan|سودان
SE|Sweden|سوئد
SG|Singapore|سنگاپور
SI|Slovenia|اسلوونی
SK|Slovakia|اسلواکی
SN|Senegal|سنگال
SO|Somalia|سومالی
SV|El Salvador|السالوادور
SY|Syria|سوریه
TG|Togo|توگو
TH|Thailand|تایلند
TJ|Tajikistan|تاجیکستان
TM|Turkmenistan|ترکمنستان
TN|Tunisia|تونس
TR|Turkey|ترکیه
TT|Trinidad and Tobago|ترینیداد و توباگو
TW|Taiwan|تایوان
TZ|Tanzania|تانزانیا
UA|Ukraine|اوکراین
UG|Uganda|اوگاندا
US|United States|آمریکا
UY|Uruguay|اروگوئه
UZ|Uzbekistan|ازبکستان
VE|Venezuela|ونزوئلا
VG|British Virgin Islands|جزایر ویرجین بریتانیا
VN|Vietnam|ویتنام
YE|Yemen|یمن
ZA|South Africa|آفریقای جنوبی
ZM|Zambia|زامبیا
ZW|Zimbabwe|زیمبابوه`

var (
	byCode = map[string]Country{}
	// nameToCode maps lower-case English and Persian names and common
	// aliases to codes.
	nameToCode = map[string]string{}
)

func init() {
	for _, line := range strings.Split(countries, "\n") {
		p := strings.Split(line, "|")
		c := Country{Code: p[0], EN: p[1], FA: p[2]}
		byCode[c.Code] = c
		nameToCode[strings.ToLower(c.EN)] = c.Code
		nameToCode[c.FA] = c.Code
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
		"آمریکا": "US", "امریکا": "US", "انگلیس": "GB", "هلند": "NL", "امارات متحده عربی": "AE",
		"کره": "KR", "ترکیه": "TR", "روسیه": "RU", "آلمان": "DE", "فرانسه": "FR", "فنلاند": "FI",
	} {
		nameToCode[alias] = code
	}
}

// Lookup returns the country for an ISO code.
func Lookup(code string) (Country, bool) {
	c, ok := byCode[strings.ToUpper(code)]
	if !ok && len(code) == 2 {
		up := strings.ToUpper(code)
		return Country{Code: up, EN: up, FA: up}, false
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
