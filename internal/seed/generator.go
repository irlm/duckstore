package seed

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// gen holds everything generated in memory before (or while) it is copied to
// Postgres. All randomness comes from one seeded RNG, so the same Options
// always produce the same data.
type gen struct {
	opt   Options
	rng   *rand.Rand
	start time.Time // first day with orders (00:00 UTC)
	end   time.Time // the dataset's "now" (exclusive)
	days  int

	countryIdx map[string]int
	currIdx    map[string]int

	fxStart time.Time
	fx      [][]float64 // [currency][day since fxStart] = units per USD, carried over weekends
	fxRows  []fxRow

	employees      []employee
	accountExecs   map[string][]int64 // region -> employee ids
	whManagers     []int64            // warehouse idx -> employee id
	whByCountry    map[string][]int64
	whByRegion     map[string][]int64
	categories     []category
	leafIdx        []int
	leafWeights    []float64
	brandsByTop    [][]int32 // top-level category idx -> brand ids
	brandNames     []string
	suppliers      []supplier
	products       []product
	popularity     []int32 // zipf rank -> product idx
	zipf           *rand.Zipf
	promotions     []promotion
	customers      []customer
	firstCustOfDay []int // customer id of the first signup on day d (days+1 entries)
}

type fxRow struct {
	curr int
	date time.Time
	rate float64
}

type employee struct {
	id, managerID         int64
	first, last           string
	title, dept, region   string
	warehouseID           int64
	hiredAt               time.Time
	salaryCents           int64
}

type supplier struct {
	name    string
	country string
	rating  int16
}

type category struct {
	id, parentID int64
	name, slug   string
	depth        int
	top          int // index of the top-level ancestor in categoryTree
	nouns        []string
	priceMin     float64
	priceMax     float64
	returnRate   float64
	season       [12]float64
}

type pricePoint struct {
	from  time.Time
	cents int64
}

type product struct {
	sku, name, desc string
	cat             int // category idx
	brand           int32
	costCents       int64
	weightKg        float64
	attrs           string
	createdAt       time.Time
	prices          []pricePoint
	quality         float64 // average star rating this product tends to get
}

func (p *product) priceAt(t time.Time) int64 {
	i, _ := slices.BinarySearchFunc(p.prices, t, func(pp pricePoint, t time.Time) int { return pp.from.Compare(t) })
	// i is the first point with from >= t. We want the last point with from <= t.
	if i < len(p.prices) && p.prices[i].from.Equal(t) {
		return p.prices[i].cents
	}
	if i == 0 {
		return p.prices[0].cents
	}
	return p.prices[i-1].cents
}

type promotion struct {
	code, name   string
	pct          float64
	starts, ends time.Time
	products     []int32 // product idx
	set          map[int32]struct{}
}

const (
	segConsumer = iota
	segBusiness
	segVIP
)

var segmentNames = []string{"consumer", "business", "vip"}

type customer struct {
	createdAt  time.Time
	country    uint8
	segment    uint8
	first      uint8
	last       uint8
	city       uint8
	street     uint8
	streetNo   uint16
	referredBy int32
	accountMgr int32
	addrID     int64 // default shipping address
	addrCount  uint8 // 1..3
	extraCity  uint8
}

func newGen(opt Options) *gen {
	end := opt.End.UTC().Truncate(24 * time.Hour)
	start := end.AddDate(-opt.Years, 0, 0)
	g := &gen{
		opt:   opt,
		rng:   rand.New(rand.NewPCG(opt.Seed, opt.Seed^0x9E3779B97F4A7C15)),
		start: start,
		end:   end,
		days:  int(end.Sub(start).Hours() / 24),
	}
	g.countryIdx = map[string]int{}
	for i, c := range countryDefs {
		g.countryIdx[c.Code] = i
	}
	g.currIdx = map[string]int{}
	for i, c := range currencyDefs {
		g.currIdx[c.Code] = i
	}
	return g
}

func (g *gen) scaled(n int, min int) int {
	return max(min, int(math.Round(float64(n)*g.opt.Scale)))
}

func (g *gen) day(d int) time.Time { return g.start.AddDate(0, 0, d) }

func (g *gen) pick(list []string) string { return list[g.rng.IntN(len(list))] }

func (g *gen) uniform(lo, hi float64) float64 { return lo + g.rng.Float64()*(hi-lo) }

func (g *gen) weighted(weights []float64) int {
	total := 0.0
	for _, w := range weights {
		total += w
	}
	r := g.rng.Float64() * total
	for i, w := range weights {
		if r < w {
			return i
		}
		r -= w
	}
	return len(weights) - 1
}

// buildReference creates all non-transactional data in memory.
func (g *gen) buildReference() {
	g.buildFX()
	g.buildWarehousesAndEmployees()
	g.buildCategories()
	g.buildBrandsAndSuppliers()
	g.buildProducts()
	g.buildPromotions()
	g.buildCustomers()
}

// --- FX rates -----------------------------------------------------------------

func (g *gen) buildFX() {
	g.fxStart = g.start.AddDate(0, 0, -10)
	n := int(g.end.Sub(g.fxStart).Hours()/24) + 1
	g.fx = make([][]float64, len(currencyDefs))
	for ci, c := range currencyDefs {
		g.fx[ci] = make([]float64, n)
		rate := c.UnitsPerUSD
		drift := g.uniform(-0.00005, 0.00012) // most currencies weaken a little against USD
		for d := range n {
			date := g.fxStart.AddDate(0, 0, d)
			if wd := date.Weekday(); wd != time.Saturday && wd != time.Sunday {
				if c.Code != "USD" {
					rate *= math.Exp(g.rng.NormFloat64()*0.004 + drift)
					rate = roundTo(rate, 6)
				}
				g.fxRows = append(g.fxRows, fxRow{ci, date, rate})
			}
			g.fx[ci][d] = rate
		}
	}
}

// rateAt returns the latest business-day rate on or before t (ASOF semantics).
func (g *gen) rateAt(curr int, t time.Time) float64 {
	d := int(t.Sub(g.fxStart).Hours() / 24)
	return g.fx[curr][min(d, len(g.fx[curr])-1)]
}

// --- Warehouses and the employee hierarchy -------------------------------------

func (g *gen) buildWarehousesAndEmployees() {
	g.whByCountry = map[string][]int64{}
	g.whByRegion = map[string][]int64{}
	for i, w := range warehouseDefs {
		id := int64(i + 1)
		g.whByCountry[w.Country] = append(g.whByCountry[w.Country], id)
		region := countryDefs[g.countryIdx[w.Country]].Region
		g.whByRegion[region] = append(g.whByRegion[region], id)
	}

	add := func(manager int64, title, dept, region string, warehouse int64, seniority int) int64 {
		id := int64(len(g.employees) + 1)
		first, last := g.pick(firstNames), g.pick(lastNames)
		// Senior people were hired earlier and earn more.
		hired := g.start.AddDate(0, 0, -g.rng.IntN(365*(seniority+1))+g.rng.IntN(max(1, g.days/(seniority+2))))
		salary := 32000 * math.Pow(1.55, float64(seniority)) * g.uniform(0.9, 1.2)
		g.employees = append(g.employees, employee{
			id: id, managerID: manager, first: first, last: last,
			title: title, dept: dept, region: region, warehouseID: warehouse,
			hiredAt: hired, salaryCents: int64(salary) * 100,
		})
		return id
	}

	ceo := add(0, "Chief Executive Officer", "Executive", "", 0, 6)
	vpSales := add(ceo, "VP of Sales", "Sales", "", 0, 5)
	vpOps := add(ceo, "VP of Operations", "Operations", "", 0, 5)
	vpPurch := add(ceo, "VP of Purchasing", "Purchasing", "", 0, 5)
	vpSupport := add(ceo, "VP of Customer Support", "Support", "", 0, 5)
	cfo := add(ceo, "Chief Financial Officer", "Finance", "", 0, 5)

	g.accountExecs = map[string][]int64{}
	for _, region := range regions {
		dir := add(vpSales, "Sales Director, "+region, "Sales", region, 0, 4)
		for range 3 {
			mgr := add(dir, "Sales Manager", "Sales", region, 0, 3)
			for range 6 {
				g.accountExecs[region] = append(g.accountExecs[region], add(mgr, "Account Executive", "Sales", region, 0, 1))
			}
		}
	}

	g.whManagers = make([]int64, len(warehouseDefs))
	for _, region := range regions {
		dir := add(vpOps, "Operations Director, "+region, "Operations", region, 0, 4)
		for _, whID := range g.whByRegion[region] {
			mgr := add(dir, "Warehouse Manager", "Operations", region, whID, 3)
			g.whManagers[whID-1] = mgr
			for range 2 {
				sup := add(mgr, "Shift Supervisor", "Operations", region, whID, 2)
				for range 6 {
					add(sup, "Warehouse Associate", "Operations", region, whID, 0)
				}
			}
		}
	}

	for range 2 {
		mgr := add(vpPurch, "Purchasing Manager", "Purchasing", "", 0, 3)
		for range 5 {
			add(mgr, "Buyer", "Purchasing", "", 0, 1)
		}
	}
	for range 2 {
		mgr := add(vpSupport, "Support Manager", "Support", "", 0, 3)
		for range 3 {
			lead := add(mgr, "Support Team Lead", "Support", "", 0, 2)
			for range 8 {
				add(lead, "Support Agent", "Support", "", 0, 0)
			}
		}
	}
	ctrl := add(cfo, "Controller", "Finance", "", 0, 3)
	for range 4 {
		add(ctrl, "Accountant", "Finance", "", 0, 1)
	}
}

// --- Categories, brands, suppliers, products ------------------------------------

func (g *gen) buildCategories() {
	var walk func(defs []catDef, parent *category, top int)
	walk = func(defs []catDef, parent *category, top int) {
		for i, def := range defs {
			c := category{
				id:   int64(len(g.categories) + 1),
				name: def.Name,
				slug: slugify(def.Name),
			}
			if parent == nil {
				top = i
				c.returnRate, c.season = def.ReturnRate, seasonProfiles[def.Season]
			} else {
				c.parentID, c.depth = parent.id, parent.depth+1
				c.returnRate, c.season = parent.returnRate, parent.season
				c.priceMin, c.priceMax = parent.priceMin, parent.priceMax
			}
			c.top = top
			if def.ReturnRate > 0 {
				c.returnRate = def.ReturnRate
			}
			if def.Season != "" {
				c.season = seasonProfiles[def.Season]
			}
			if def.PriceMax > 0 {
				c.priceMin, c.priceMax = def.PriceMin, def.PriceMax
			}
			c.nouns = def.Nouns
			g.categories = append(g.categories, c)
			idx := len(g.categories) - 1
			if len(def.Children) == 0 {
				g.leafIdx = append(g.leafIdx, idx)
				weight := 1.0
				switch categoryTree[top].Name {
				case "Books":
					weight = 2.5
				case "Fashion":
					weight = 2
				}
				g.leafWeights = append(g.leafWeights, weight)
			}
			walk(def.Children, &g.categories[idx], top)
		}
	}
	walk(categoryTree, nil, 0)
}

func (g *gen) buildBrandsAndSuppliers() {
	for _, p := range brandPrefixes {
		for _, s := range brandSuffixes {
			g.brandNames = append(g.brandNames, p+s)
		}
	}
	g.rng.Shuffle(len(g.brandNames), func(i, j int) { g.brandNames[i], g.brandNames[j] = g.brandNames[j], g.brandNames[i] })
	// Each brand focuses on one or two top-level categories.
	g.brandsByTop = make([][]int32, len(categoryTree))
	for b := range g.brandNames {
		id := int32(b + 1)
		t1, t2 := g.rng.IntN(len(categoryTree)), g.rng.IntN(len(categoryTree))
		g.brandsByTop[t1] = append(g.brandsByTop[t1], id)
		if t2 != t1 {
			g.brandsByTop[t2] = append(g.brandsByTop[t2], id)
		}
	}
	for t := range g.brandsByTop {
		if len(g.brandsByTop[t]) == 0 {
			g.brandsByTop[t] = []int32{int32(t + 1)}
		}
	}

	supplierCountries := []string{"US", "DE", "JP", "IN", "MX", "BR", "GB", "ES", "SG", "CA"}
	for _, w := range supplierWords {
		for _, k := range supplierKinds {
			g.suppliers = append(g.suppliers, supplier{
				name:    w + " " + k,
				country: g.pick(supplierCountries),
				rating:  int16(1 + g.weighted([]float64{2, 5, 20, 45, 28})),
			})
		}
	}
}

func (g *gen) buildProducts() {
	n := g.scaled(30000, 500)
	g.products = make([]product, n)
	span := g.end.Sub(g.start)
	for i := range g.products {
		p := &g.products[i]
		p.cat = g.leafIdx[g.weighted(g.leafWeights)]
		c := &g.categories[p.cat]
		top := categoryTree[c.top].Name
		brands := g.brandsByTop[c.top]
		p.brand = brands[g.rng.IntN(len(brands))]
		noun := g.pick(c.nouns)
		model := g.pick([]string{"", " 2", " 3", " X", " S", " 14", " 16", " II", " 500", " Go"})
		adj := g.pick(productAdjectives)
		brand := g.brandNames[p.brand-1]
		p.name = fmt.Sprintf("%s %s %s%s", brand, adj, noun, model)
		p.sku = fmt.Sprintf("%s-%06d", strings.ToUpper(slugify(top)[:3]), i+1)
		color, material := g.pick(colors), g.pick(materials)
		p.desc = fmt.Sprintf("The %s is a %s %s by %s, made of %s.", p.name, color, strings.ToLower(noun), brand, material)

		attrs := fmt.Sprintf(`{"color": %q, "material": %q, "warranty_months": %d`, color, material, []int{0, 12, 24}[g.rng.IntN(3)])
		if top == "Fashion" {
			attrs += `, "sizes": ["XS", "S", "M", "L", "XL"]`
		}
		if top == "Electronics" {
			attrs += fmt.Sprintf(`, "energy_class": %q`, g.pick([]string{"A", "B", "C"}))
		}
		p.attrs = attrs + "}"

		// Log-uniform price inside the category range, with a .99 ending.
		price := math.Exp(g.uniform(math.Log(c.priceMin), math.Log(c.priceMax)))
		cents := charmPrice(price)
		p.costCents = int64(float64(cents) * g.uniform(0.45, 0.7))
		p.weightKg = roundTo(math.Sqrt(price)*g.uniform(0.02, 0.2), 3)
		p.quality = 2.6 + 2.3*math.Sqrt(g.rng.Float64())

		if g.rng.Float64() < 0.75 {
			p.createdAt = g.start.Add(-time.Duration(g.rng.Int64N(int64(400 * 24 * time.Hour))))
		} else {
			p.createdAt = g.start.Add(time.Duration(g.rng.Int64N(int64(span - 45*24*time.Hour))))
		}
		p.createdAt = p.createdAt.Truncate(time.Second)
		p.prices = []pricePoint{{p.createdAt, cents}}
		life := g.end.Sub(p.createdAt)
		changes := int(life.Hours() / 24 / 365 * g.uniform(0, 2.5))
		var at []time.Time
		for range changes {
			if life > 60*24*time.Hour {
				at = append(at, p.createdAt.Add(30*24*time.Hour+time.Duration(g.rng.Int64N(int64(life-30*24*time.Hour)))).Truncate(time.Second))
			}
		}
		slices.SortFunc(at, time.Time.Compare)
		for _, t := range at {
			if t.Equal(p.prices[len(p.prices)-1].from) {
				continue
			}
			cents = charmPrice(float64(cents) / 100 * g.uniform(0.88, 1.15))
			p.prices = append(p.prices, pricePoint{t, cents})
		}
	}

	// Popularity: products ranked by a noisy "cheap sells more" score, then
	// sampled with a Zipf distribution, so a few products sell a lot and most
	// sell a little (the long tail).
	score := make([]float64, n)
	g.popularity = make([]int32, n)
	for i := range g.popularity {
		g.popularity[i] = int32(i)
		score[i] = 0.9*math.Log(float64(g.products[i].prices[0].cents)) + 1.2*g.rng.NormFloat64()
	}
	slices.SortFunc(g.popularity, func(a, b int32) int { return cmp.Compare(score[a], score[b]) })
	g.zipf = rand.NewZipf(g.rng, 1.1, 20, uint64(n-1))
}

// --- Promotions ------------------------------------------------------------------

func (g *gen) buildPromotions() {
	productsUnder := func(names ...string) []int32 {
		want := map[int]bool{}
		for _, name := range names {
			for i, c := range g.categories {
				if c.name == name {
					want[i] = true
				}
			}
		}
		// Add descendants: categories are stored parent-before-child, so one pass works.
		for i, c := range g.categories {
			if c.parentID != 0 && want[int(c.parentID-1)] {
				want[i] = true
			}
		}
		var out []int32
		for i, p := range g.products {
			if want[p.cat] {
				out = append(out, int32(i))
			}
		}
		return out
	}
	randomShare := func(share float64) []int32 {
		var out []int32
		for i := range g.products {
			if g.rng.Float64() < share {
				out = append(out, int32(i))
			}
		}
		return out
	}
	add := func(code, name string, pct float64, starts, ends time.Time, prods []int32) {
		if ends.Before(g.start) || !starts.Before(g.end) || len(prods) == 0 {
			return
		}
		set := make(map[int32]struct{}, len(prods))
		for _, p := range prods {
			set[p] = struct{}{}
		}
		g.promotions = append(g.promotions, promotion{code, name, pct, starts, ends, prods, set})
	}

	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	for y := g.start.Year(); y <= g.end.Year(); y++ {
		bf := blackFriday(y)
		add(fmt.Sprintf("BLACKFRIDAY%d", y), fmt.Sprintf("Black Friday %d", y), 25, bf, bf.AddDate(0, 0, 4), randomShare(0.3))
		add(fmt.Sprintf("GIFTS%d", y), fmt.Sprintf("Holiday Gifts %d", y), 15, date(y, 12, 1), date(y, 12, 21), productsUnder("Toys", "Gaming", "Kids Books"))
		add(fmt.Sprintf("NEWYOU%d", y), fmt.Sprintf("New Year Fitness %d", y), 20, date(y, 1, 1), date(y, 2, 1), productsUnder("Fitness"))
		add(fmt.Sprintf("SPRINGHOME%d", y), fmt.Sprintf("Spring Home %d", y), 15, date(y, 4, 1), date(y, 4, 21), productsUnder("Home & Kitchen"))
		add(fmt.Sprintf("SUMMER%d", y), fmt.Sprintf("Summer Sale %d", y), 20, date(y, 7, 1), date(y, 7, 16), productsUnder("Camping", "Cycling", "Outdoor Toys", "Sunglasses"))
		add(fmt.Sprintf("SCHOOL%d", y), fmt.Sprintf("Back to School %d", y), 10, date(y, 8, 15), date(y, 9, 11), productsUnder("Computers", "Bags"))
		for m := 1; m <= 12; m++ {
			brand := int32(1 + g.rng.IntN(len(g.brandNames)))
			var prods []int32
			for i, p := range g.products {
				if p.brand == brand {
					prods = append(prods, int32(i))
				}
			}
			s := date(y, time.Month(m), 5+g.rng.IntN(15))
			add(fmt.Sprintf("FLASH-%d-%02d", y, m), fmt.Sprintf("Flash sale: %s", g.brandNames[brand-1]),
				float64(10+5*g.rng.IntN(5)), s, s.AddDate(0, 0, 3), prods)
		}
	}
}

func (g *gen) activePromotions(t time.Time) []int {
	var out []int
	for i := range g.promotions {
		if !t.Before(g.promotions[i].starts) && t.Before(g.promotions[i].ends) {
			out = append(out, i)
		}
	}
	return out
}

// --- Customers -----------------------------------------------------------------

func (g *gen) buildCustomers() {
	n := g.scaled(300000, 2000)
	weights := make([]float64, g.days)
	total := 0.0
	for d := range weights {
		w := (1 + 1.2*float64(d)/float64(g.days)) * g.uniform(0.8, 1.2)
		if d < 14 {
			w *= 8 // launch campaign
		}
		if m := g.day(d).Month(); m == time.November || m == time.December {
			w *= 1.3
		}
		weights[d] = w
		total += w
	}

	countryWeights := make([]float64, len(countryDefs))
	for i, c := range countryDefs {
		countryWeights[i] = c.Weight
	}

	g.customers = make([]customer, 0, n)
	g.firstCustOfDay = make([]int, g.days+1)
	carry := 0.0
	var addrID int64
	for d := range g.days {
		g.firstCustOfDay[d] = len(g.customers) + 1
		exact := weights[d]/total*float64(n) + carry
		count := int(exact)
		carry = exact - float64(count)
		if d == g.days-1 {
			count = n - len(g.customers)
		}
		secs := make([]int, count)
		for i := range secs {
			secs[i] = g.rng.IntN(86400)
		}
		slices.Sort(secs)
		for _, s := range secs {
			id := len(g.customers) + 1
			c := customer{
				createdAt: g.day(d).Add(time.Duration(s) * time.Second),
				country:   uint8(g.weighted(countryWeights)),
				first:     uint8(g.rng.IntN(len(firstNames))),
				last:      uint8(g.rng.IntN(len(lastNames))),
				street:    uint8(g.rng.IntN(len(streetNames))),
				streetNo:  uint16(1 + g.rng.IntN(2500)),
			}
			cd := &countryDefs[c.country]
			c.city = uint8(g.rng.IntN(len(cd.Cities)))
			c.extraCity = uint8(g.rng.IntN(len(cd.Cities)))
			c.segment = uint8(g.weighted([]float64{85, 12, 3}))
			if c.segment != segConsumer {
				execs := g.accountExecs[cd.Region]
				c.accountMgr = int32(execs[g.rng.IntN(len(execs))])
			}
			if id > 500 && g.rng.Float64() < 0.12 {
				c.referredBy = int32(max(1, id-1-g.rng.IntN(min(id-1, 50000))))
			}
			c.addrCount = uint8(1 + g.weighted([]float64{60, 28, 12}))
			c.addrID = addrID + 1
			addrID += int64(c.addrCount)
			g.customers = append(g.customers, c)
		}
	}
	g.firstCustOfDay[g.days] = len(g.customers) + 1
}

// --- helpers ----------------------------------------------------------------------

func roundTo(x float64, decimals int) float64 {
	p := math.Pow(10, float64(decimals))
	return math.Round(x*p) / p
}

// charmPrice turns 23.4 into 22.99 or 23.99 (prices ending in .99), in cents.
func charmPrice(price float64) int64 {
	if price < 5 {
		return max(99, int64(math.Round(price*100)))
	}
	return int64(math.Round(price))*100 - 1
}

var accentFolder = strings.NewReplacer(
	"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n", "&", "and",
)

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range accentFolder.Replace(strings.ToLower(s)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func blackFriday(year int) time.Time {
	nov1 := time.Date(year, time.November, 1, 0, 0, 0, 0, time.UTC)
	offset := (int(time.Thursday) - int(nov1.Weekday()) + 7) % 7
	return nov1.AddDate(0, 0, offset+21+1)
}
