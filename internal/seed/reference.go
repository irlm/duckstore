package seed

// Static reference data used by the generator.

type currencyDef struct {
	Code, Name, Symbol string
	UnitsPerUSD        float64 // starting rate, then a random walk
}

var currencyDefs = []currencyDef{
	{"USD", "US Dollar", "$", 1},
	{"CAD", "Canadian Dollar", "C$", 1.36},
	{"MXN", "Mexican Peso", "MX$", 18.5},
	{"BRL", "Brazilian Real", "R$", 5.3},
	{"CLP", "Chilean Peso", "CLP$", 930},
	{"GBP", "Pound Sterling", "£", 0.79},
	{"EUR", "Euro", "€", 0.92},
	{"SEK", "Swedish Krona", "kr", 10.6},
	{"JPY", "Japanese Yen", "¥", 148},
	{"AUD", "Australian Dollar", "A$", 1.52},
	{"INR", "Indian Rupee", "₹", 83.5},
	{"SGD", "Singapore Dollar", "S$", 1.34},
}

const (
	regionNA    = "North America"
	regionLatAm = "Latin America"
	regionEU    = "Europe"
	regionAPAC  = "Asia Pacific"
)

var regions = []string{regionNA, regionLatAm, regionEU, regionAPAC}

type countryDef struct {
	Code, Name, Region, Currency string
	TaxRate                      float64
	Weight                       float64 // share of customers
	Cities                       []string
	Carriers                     []string
}

var countryDefs = []countryDef{
	{"US", "United States", regionNA, "USD", 0.07, 30, []string{"New York", "Los Angeles", "Chicago", "Houston", "Seattle", "Miami", "Denver", "Boston"}, []string{"UPS", "FedEx", "USPS"}},
	{"CA", "Canada", regionNA, "CAD", 0.13, 5, []string{"Toronto", "Vancouver", "Montreal", "Calgary"}, []string{"Canada Post", "UPS", "FedEx"}},
	{"MX", "Mexico", regionLatAm, "MXN", 0.16, 4, []string{"Mexico City", "Guadalajara", "Monterrey", "Puebla"}, []string{"Estafeta", "DHL"}},
	{"BR", "Brazil", regionLatAm, "BRL", 0.17, 8, []string{"São Paulo", "Rio de Janeiro", "Belo Horizonte", "Curitiba", "Porto Alegre", "Recife"}, []string{"Correios", "DHL"}},
	{"CL", "Chile", regionLatAm, "CLP", 0.19, 2, []string{"Santiago", "Valparaíso", "Concepción"}, []string{"Chilexpress", "DHL"}},
	{"GB", "United Kingdom", regionEU, "GBP", 0.20, 8, []string{"London", "Manchester", "Birmingham", "Edinburgh", "Bristol"}, []string{"Royal Mail", "DPD", "DHL"}},
	{"DE", "Germany", regionEU, "EUR", 0.19, 8, []string{"Berlin", "Munich", "Hamburg", "Cologne", "Frankfurt"}, []string{"DHL", "DPD", "GLS"}},
	{"FR", "France", regionEU, "EUR", 0.20, 5, []string{"Paris", "Lyon", "Marseille", "Toulouse"}, []string{"DPD", "GLS", "DHL"}},
	{"ES", "Spain", regionEU, "EUR", 0.21, 4, []string{"Madrid", "Barcelona", "Valencia", "Seville"}, []string{"GLS", "DHL"}},
	{"IT", "Italy", regionEU, "EUR", 0.22, 3, []string{"Rome", "Milan", "Naples", "Turin"}, []string{"GLS", "DHL"}},
	{"NL", "Netherlands", regionEU, "EUR", 0.21, 2, []string{"Amsterdam", "Rotterdam", "Utrecht"}, []string{"DPD", "DHL"}},
	{"PT", "Portugal", regionEU, "EUR", 0.23, 2, []string{"Lisbon", "Porto", "Braga"}, []string{"GLS", "DHL"}},
	{"SE", "Sweden", regionEU, "SEK", 0.25, 2, []string{"Stockholm", "Gothenburg", "Malmö"}, []string{"DHL", "DPD"}},
	{"JP", "Japan", regionAPAC, "JPY", 0.10, 6, []string{"Tokyo", "Osaka", "Nagoya", "Sapporo", "Fukuoka"}, []string{"Yamato", "DHL"}},
	{"AU", "Australia", regionAPAC, "AUD", 0.10, 4, []string{"Sydney", "Melbourne", "Brisbane", "Perth"}, []string{"Australia Post", "DHL"}},
	{"IN", "India", regionAPAC, "INR", 0.18, 5, []string{"Mumbai", "Delhi", "Bengaluru", "Pune", "Chennai"}, []string{"Blue Dart", "DHL"}},
	{"SG", "Singapore", regionAPAC, "SGD", 0.09, 2, []string{"Singapore"}, []string{"Ninja Van", "DHL"}},
}

// Base transit days per carrier (same country). Cross-border adds more.
var carrierTransitDays = map[string]int{
	"UPS": 2, "FedEx": 2, "USPS": 4, "Canada Post": 4, "Estafeta": 4, "Correios": 6,
	"Chilexpress": 3, "Royal Mail": 3, "DPD": 3, "GLS": 4, "DHL": 3, "Yamato": 1,
	"Australia Post": 4, "Blue Dart": 3, "Ninja Van": 2,
}

type warehouseDef struct {
	Code, Name, Country, City string
}

var warehouseDefs = []warehouseDef{
	{"US-NJ", "Newark Fulfillment Center", "US", "Newark"},
	{"US-NV", "Reno Fulfillment Center", "US", "Reno"},
	{"CA-ON", "Toronto Distribution Center", "CA", "Toronto"},
	{"MX-NL", "Monterrey Distribution Center", "MX", "Monterrey"},
	{"BR-SP", "São Paulo Fulfillment Center", "BR", "Cajamar"},
	{"GB-CV", "Coventry Fulfillment Center", "GB", "Coventry"},
	{"DE-LE", "Leipzig Hub", "DE", "Leipzig"},
	{"ES-MD", "Madrid Distribution Center", "ES", "Getafe"},
	{"JP-OS", "Osaka Fulfillment Center", "JP", "Osaka"},
	{"AU-NS", "Sydney Distribution Center", "AU", "Sydney"},
	{"IN-PN", "Pune Fulfillment Center", "IN", "Pune"},
	{"SG-SG", "Singapore Hub", "SG", "Singapore"},
}

var firstNames = []string{
	"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda", "David", "Elizabeth",
	"William", "Barbara", "Richard", "Susan", "Joseph", "Jessica", "Thomas", "Sarah", "Carlos", "Maria",
	"Lucas", "Ana", "Gabriel", "Julia", "Mateus", "Beatriz", "Rafael", "Camila", "Diego", "Valentina",
	"Oliver", "Amelia", "Harry", "Isla", "George", "Emily", "Noah", "Sophie", "Leon", "Mia",
	"Felix", "Hannah", "Lukas", "Lena", "Hugo", "Chloé", "Louis", "Camille", "Pablo", "Lucía",
	"Alejandro", "Sofía", "Marco", "Giulia", "Luca", "Chiara", "Daan", "Emma", "Tiago", "Inês",
	"Erik", "Astrid", "Haruto", "Yui", "Sota", "Hina", "Jack", "Charlotte", "Aarav", "Ananya",
	"Vihaan", "Diya", "Wei", "Mei", "Ethan", "Olivia", "Liam", "Ava", "Mason", "Isabella",
}

var lastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez",
	"Silva", "Santos", "Oliveira", "Souza", "Costa", "Pereira", "Almeida", "Ferreira", "Lima", "Carvalho",
	"Taylor", "Wilson", "Evans", "Thomas", "Roberts", "Walker", "Müller", "Schmidt", "Schneider", "Fischer",
	"Weber", "Becker", "Martin", "Bernard", "Dubois", "Durand", "Lefebvre", "Moreau", "Fernández", "López",
	"González", "Sánchez", "Pérez", "Rossi", "Russo", "Ferrari", "Esposito", "de Jong", "Jansen", "de Vries",
	"Andersson", "Johansson", "Karlsson", "Sato", "Suzuki", "Takahashi", "Tanaka", "Watanabe", "Nguyen", "Lee",
	"Patel", "Sharma", "Singh", "Kumar", "Gupta", "Tan", "Lim", "Chen", "Wong", "Anderson",
	"Moore", "Jackson", "White", "Harris", "Clark", "Lewis", "Young", "King", "Wright", "Hill",
}

var streetNames = []string{
	"Main St", "Oak Ave", "Park Rd", "Maple Dr", "Cedar Ln", "Elm St", "High St", "Station Rd", "Church St", "Lake View",
	"River Rd", "Hill St", "Market Sq", "Garden Way", "Sunset Blvd", "King St", "Queen St", "Bridge Rd", "Mill Ln", "Forest Ave",
}

var brandPrefixes = []string{"Nord", "Apex", "Lumi", "Terra", "Vela", "Orbi", "Kai", "Zen", "Brio", "Nova", "Aero", "Pola", "Ember", "Quanta", "Sol", "Echo", "Iron", "Maple", "Coral", "Summit"}
var brandSuffixes = []string{"vik", "tech", "works", "line", "craft", "ora", "wave", "gear", "nest", "labs", "form", "mode", "peak", "ware", "co"}

var supplierWords = []string{"Pacific", "Atlas", "Golden", "Northern", "Silver", "Eastern", "Prime", "Global", "United", "Royal", "Delta", "Horizon", "Pioneer", "Liberty", "Crown", "Sterling", "Evergreen", "Harbor", "Summit", "Keystone"}
var supplierKinds = []string{"Trading", "Supply", "Imports", "Manufacturing", "Distribution", "Industries", "Wholesale", "Sourcing", "Logistics", "Goods", "Partners", "Merchants", "Holdings", "Exports", "Components", "Materials", "Textiles", "Electronics", "Foods", "Works"}

var productAdjectives = []string{"Classic", "Pro", "Ultra", "Lite", "Max", "Air", "Prime", "Eco", "Smart", "Plus", "Mini", "Elite", "Sport", "Studio", "Essential", "Signature", "Urban", "Compact", "Deluxe", "Active"}
var colors = []string{"black", "white", "silver", "navy", "red", "green", "beige", "gray", "blue", "pink"}
var materials = []string{"aluminum", "steel", "cotton", "leather", "plastic", "wood", "glass", "ceramic", "polyester", "bamboo"}

// Seasonality profiles: demand multiplier per month (Jan..Dec).
var seasonProfiles = map[string][12]float64{
	"flat":    {1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	"gifts":   {0.9, 0.9, 0.95, 0.95, 1, 1, 1, 1, 1, 1.05, 1.3, 1.5},
	"holiday": {0.75, 0.75, 0.85, 0.85, 0.9, 0.9, 0.9, 0.95, 1, 1.1, 1.7, 2.3},
	"summer":  {0.6, 0.7, 0.9, 1.1, 1.4, 1.6, 1.6, 1.3, 1, 0.8, 0.7, 0.7},
	"newyear": {1.9, 1.4, 1.1, 1, 0.9, 0.9, 0.8, 0.8, 0.9, 0.9, 1, 1.1},
}

// catDef describes the category tree. Leaf settings are inherited from the
// closest ancestor that sets them.
type catDef struct {
	Name       string
	Children   []catDef
	Nouns      []string // product nouns (leaves)
	PriceMin   float64
	PriceMax   float64
	ReturnRate float64
	Season     string
}

var categoryTree = []catDef{
	{Name: "Electronics", ReturnRate: 0.07, Season: "gifts", Children: []catDef{
		{Name: "Computers", Children: []catDef{
			{Name: "Laptops", Nouns: []string{"Laptop", "Notebook", "Ultrabook"}, PriceMin: 400, PriceMax: 2500},
			{Name: "Desktops", Nouns: []string{"Desktop PC", "Mini PC", "Workstation"}, PriceMin: 350, PriceMax: 2200},
			{Name: "Monitors", Nouns: []string{"Monitor", "Display"}, PriceMin: 120, PriceMax: 900},
			{Name: "Computer Accessories", Children: []catDef{
				{Name: "Keyboards", Nouns: []string{"Keyboard", "Mechanical Keyboard"}, PriceMin: 20, PriceMax: 180},
				{Name: "Mice", Nouns: []string{"Mouse", "Trackball"}, PriceMin: 10, PriceMax: 120},
				{Name: "Webcams", Nouns: []string{"Webcam"}, PriceMin: 30, PriceMax: 200},
			}},
		}},
		{Name: "Phones", Children: []catDef{
			{Name: "Smartphones", Nouns: []string{"Smartphone", "Phone"}, PriceMin: 150, PriceMax: 1400},
			{Name: "Phone Cases", Nouns: []string{"Phone Case", "Folio Case"}, PriceMin: 8, PriceMax: 60, ReturnRate: 0.05},
			{Name: "Chargers", Nouns: []string{"Charger", "Power Bank", "USB-C Cable"}, PriceMin: 8, PriceMax: 80},
		}},
		{Name: "Audio", Children: []catDef{
			{Name: "Headphones", Nouns: []string{"Headphones", "Earbuds"}, PriceMin: 20, PriceMax: 450},
			{Name: "Speakers", Nouns: []string{"Speaker", "Soundbar"}, PriceMin: 25, PriceMax: 600},
		}},
		{Name: "Cameras", Children: []catDef{
			{Name: "Digital Cameras", Nouns: []string{"Camera", "Mirrorless Camera"}, PriceMin: 300, PriceMax: 2500},
			{Name: "Lenses", Nouns: []string{"Lens", "Zoom Lens"}, PriceMin: 150, PriceMax: 1800},
		}},
		{Name: "Gaming", Season: "holiday", Children: []catDef{
			{Name: "Consoles", Nouns: []string{"Console", "Handheld Console"}, PriceMin: 250, PriceMax: 600},
			{Name: "Video Games", Nouns: []string{"Game"}, PriceMin: 20, PriceMax: 70, ReturnRate: 0.02},
			{Name: "Controllers", Nouns: []string{"Controller", "Gamepad"}, PriceMin: 25, PriceMax: 180},
		}},
	}},
	{Name: "Home & Kitchen", ReturnRate: 0.05, Season: "flat", Children: []catDef{
		{Name: "Kitchen", Children: []catDef{
			{Name: "Cookware", Nouns: []string{"Frying Pan", "Stock Pot", "Skillet", "Dutch Oven"}, PriceMin: 15, PriceMax: 300},
			{Name: "Small Appliances", Nouns: []string{"Blender", "Toaster", "Coffee Maker", "Air Fryer"}, PriceMin: 25, PriceMax: 400, Season: "gifts"},
			{Name: "Cutlery", Nouns: []string{"Knife Set", "Chef Knife"}, PriceMin: 15, PriceMax: 250},
		}},
		{Name: "Furniture", ReturnRate: 0.08, Children: []catDef{
			{Name: "Chairs", Nouns: []string{"Office Chair", "Dining Chair"}, PriceMin: 60, PriceMax: 700},
			{Name: "Desks", Nouns: []string{"Desk", "Standing Desk"}, PriceMin: 120, PriceMax: 900},
			{Name: "Shelves", Nouns: []string{"Bookshelf", "Wall Shelf"}, PriceMin: 40, PriceMax: 350},
		}},
		{Name: "Bedding", Nouns: []string{"Duvet", "Pillow", "Sheet Set"}, PriceMin: 20, PriceMax: 250},
		{Name: "Decor", Nouns: []string{"Lamp", "Rug", "Wall Art", "Vase"}, PriceMin: 15, PriceMax: 300, Season: "gifts"},
	}},
	{Name: "Fashion", ReturnRate: 0.18, Season: "flat", Children: []catDef{
		{Name: "Men", Children: []catDef{
			{Name: "Men's Shirts", Nouns: []string{"Shirt", "Polo", "T-Shirt"}, PriceMin: 12, PriceMax: 90},
			{Name: "Men's Pants", Nouns: []string{"Jeans", "Chinos", "Trousers"}, PriceMin: 25, PriceMax: 120},
			{Name: "Men's Shoes", Nouns: []string{"Sneakers", "Boots", "Loafers"}, PriceMin: 40, PriceMax: 250},
		}},
		{Name: "Women", Children: []catDef{
			{Name: "Dresses", Nouns: []string{"Dress", "Maxi Dress"}, PriceMin: 25, PriceMax: 220, ReturnRate: 0.24},
			{Name: "Women's Tops", Nouns: []string{"Blouse", "Top", "Sweater"}, PriceMin: 15, PriceMax: 120},
			{Name: "Women's Shoes", Nouns: []string{"Heels", "Sneakers", "Sandals", "Boots"}, PriceMin: 35, PriceMax: 280},
		}},
		{Name: "Fashion Accessories", ReturnRate: 0.10, Children: []catDef{
			{Name: "Bags", Nouns: []string{"Backpack", "Tote Bag", "Handbag"}, PriceMin: 20, PriceMax: 400},
			{Name: "Watches", Nouns: []string{"Watch", "Smartwatch"}, PriceMin: 40, PriceMax: 900, Season: "gifts"},
			{Name: "Sunglasses", Nouns: []string{"Sunglasses"}, PriceMin: 15, PriceMax: 250, Season: "summer"},
		}},
	}},
	{Name: "Sports & Outdoors", ReturnRate: 0.06, Season: "flat", Children: []catDef{
		{Name: "Fitness", Season: "newyear", Children: []catDef{
			{Name: "Weights", Nouns: []string{"Dumbbell Set", "Kettlebell"}, PriceMin: 20, PriceMax: 400},
			{Name: "Yoga", Nouns: []string{"Yoga Mat", "Yoga Block"}, PriceMin: 10, PriceMax: 90},
			{Name: "Cardio", Nouns: []string{"Treadmill", "Exercise Bike", "Jump Rope"}, PriceMin: 15, PriceMax: 1500},
		}},
		{Name: "Camping", Season: "summer", Children: []catDef{
			{Name: "Tents", Nouns: []string{"Tent", "Dome Tent"}, PriceMin: 60, PriceMax: 600},
			{Name: "Sleeping Bags", Nouns: []string{"Sleeping Bag"}, PriceMin: 30, PriceMax: 300},
		}},
		{Name: "Cycling", Season: "summer", Children: []catDef{
			{Name: "Bikes", Nouns: []string{"Road Bike", "Mountain Bike", "E-Bike"}, PriceMin: 350, PriceMax: 3500},
			{Name: "Helmets", Nouns: []string{"Helmet"}, PriceMin: 30, PriceMax: 200},
		}},
	}},
	{Name: "Books", ReturnRate: 0.02, Season: "gifts", Children: []catDef{
		{Name: "Fiction", Nouns: []string{"Novel", "Thriller"}, PriceMin: 8, PriceMax: 30},
		{Name: "Non-fiction", Children: []catDef{
			{Name: "Business", Nouns: []string{"Business Book"}, PriceMin: 12, PriceMax: 45},
			{Name: "Science", Nouns: []string{"Science Book"}, PriceMin: 12, PriceMax: 60},
			{Name: "History", Nouns: []string{"History Book"}, PriceMin: 12, PriceMax: 50},
		}},
		{Name: "Kids Books", Nouns: []string{"Picture Book", "Story Book"}, PriceMin: 6, PriceMax: 25, Season: "holiday"},
	}},
	{Name: "Beauty", ReturnRate: 0.03, Season: "gifts", Children: []catDef{
		{Name: "Skincare", Nouns: []string{"Moisturizer", "Serum", "Cleanser"}, PriceMin: 8, PriceMax: 120},
		{Name: "Makeup", Nouns: []string{"Lipstick", "Foundation", "Mascara"}, PriceMin: 6, PriceMax: 70},
		{Name: "Haircare", Nouns: []string{"Shampoo", "Hair Dryer", "Conditioner"}, PriceMin: 6, PriceMax: 250},
	}},
	{Name: "Toys", ReturnRate: 0.04, Season: "holiday", Children: []catDef{
		{Name: "Building Sets", Nouns: []string{"Building Set"}, PriceMin: 15, PriceMax: 400},
		{Name: "Dolls", Nouns: []string{"Doll", "Dollhouse"}, PriceMin: 10, PriceMax: 150},
		{Name: "Puzzles & Games", Nouns: []string{"Puzzle", "Board Game"}, PriceMin: 10, PriceMax: 80},
		{Name: "Outdoor Toys", Nouns: []string{"Scooter", "Trampoline", "Water Blaster"}, PriceMin: 15, PriceMax: 400, Season: "summer"},
	}},
	{Name: "Grocery", ReturnRate: 0.005, Season: "flat", Children: []catDef{
		{Name: "Coffee & Tea", Nouns: []string{"Coffee Beans", "Tea Box", "Espresso Pods"}, PriceMin: 6, PriceMax: 45},
		{Name: "Snacks", Nouns: []string{"Snack Box", "Chocolate Pack"}, PriceMin: 3, PriceMax: 30},
		{Name: "Pantry", Nouns: []string{"Olive Oil", "Pasta Pack", "Spice Set"}, PriceMin: 3, PriceMax: 40},
	}},
}

var reviewTitles = [6][]string{
	1: {"Terrible", "Do not buy", "Broke quickly", "Waste of money"},
	2: {"Disappointed", "Not great", "Expected more", "Poor quality"},
	3: {"It's okay", "Average", "Does the job", "Mixed feelings"},
	4: {"Very good", "Solid purchase", "Happy with it", "Good value"},
	5: {"Excellent!", "Love it", "Exceeded expectations", "Highly recommend"},
}

var reviewBodies = [6][]string{
	1: {"Stopped working after a week.", "Nothing like the pictures.", "Arrived damaged and support was slow."},
	2: {"Quality is lower than I expected for the price.", "Works, but feels cheap.", "Had to return it once already."},
	3: {"Does what it says, nothing special.", "Fine for the price.", "Some good points, some bad points."},
	4: {"Good quality and fast delivery.", "Would buy again.", "Works well, small details could be better."},
	5: {"Perfect, exactly what I needed.", "Great quality, fast shipping.", "Best purchase this year."},
}
