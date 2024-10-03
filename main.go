package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/xuri/excelize/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
	"gopkg.in/mgo.v2/bson"
)

// Stock represents the data of a stock
type Stock struct {
	Name            string
	PE              float64
	MarketCap       float64
	DividendYield   float64
	ROCE            float64
	QuarterlySales  float64
	QuarterlyProfit float64
	Cons            []string
	Pros            []string
}

// Peer represents the data of a peer stock
type Peer struct {
	Name            string
	PE              float64
	MarketCap       float64
	DividendYield   float64
	ROCE            float64
	QuarterlySales  float64
	QuarterlyProfit float64
}

// QuarterlyData holds historical data for a stock
type QuarterlyData struct {
	NetProfit        float64
	Sales            float64
	TotalLiabilities float64
	ROCE             float64
}

// compareWithPeers calculates a peer comparison score
func compareWithPeers(stock Stock, peers interface{}) float64 {
	peerScore := 0.0
	var medianScore float64

	if arr, ok := peers.(primitive.A); ok {
		// Ensure there are enough peers to compare
		if len(arr) < 2 {
			zap.L().Warn("Not enough peers to compare")
			return 0.0
		}

		for _, peerRaw := range arr[:len(arr)-1] {
			peer := peerRaw.(bson.M)

			// Parse peer values to float64
			peerPE := parseFloat(peer["pe"])
			peerMarketCap := parseFloat(peer["market_cap"])
			peerDividendYield := parseFloat(peer["div_yield"])
			peerROCE := parseFloat(peer["roce"])
			peerQuarterlySales := parseFloat(peer["sales_qtr"])
			peerQuarterlyProfit := parseFloat(peer["np_qtr"])

			// Example scoring logic
			if stock.PE < peerPE {
				peerScore += 10
			} else {
				peerScore += math.Max(0, 10-(stock.PE-peerPE))
			}

			if stock.MarketCap > peerMarketCap {
				peerScore += 5
			}

			if stock.DividendYield > peerDividendYield {
				peerScore += 5
			}

			if stock.ROCE > peerROCE {
				peerScore += 10
			}

			if stock.QuarterlySales > peerQuarterlySales {
				peerScore += 5
			}

			if stock.QuarterlyProfit > peerQuarterlyProfit {
				peerScore += 10
			}
		}
		medianRaw := arr[len(arr)-1]
		median := medianRaw.(bson.M)

		// Parse median values to float64
		medianPE := parseFloat(median["pe"])
		medianMarketCap := parseFloat(median["market_cap"])
		medianDividendYield := parseFloat(median["div_yield"])
		medianROCE := parseFloat(median["roce"])
		medianQuarterlySales := parseFloat(median["sales_qtr"])
		medianQuarterlyProfit := parseFloat(median["np_qtr"])

		// Adjust score based on median comparison
		if stock.PE < medianPE {
			peerScore += 5
		} else {
			peerScore += math.Max(0, 5-(stock.PE-medianPE))
		}

		if stock.MarketCap > medianMarketCap {
			peerScore += 3
		}

		if stock.DividendYield > medianDividendYield {
			peerScore += 3
		}

		if stock.ROCE > medianROCE {
			peerScore += 5
		}

		if stock.QuarterlySales > medianQuarterlySales {
			peerScore += 2
		}

		if stock.QuarterlyProfit > medianQuarterlyProfit {
			peerScore += 5
		}

		// Normalize by the number of peers (excluding the median)
		peerCount := len(arr) - 1
		if peerCount > 0 {
			return peerScore / float64(peerCount)
		}

		// Normalize by the number of peers excluding the last element
	}

	// Combine peerScore with medianScore (example: giving 10% weight to the median)
	finalScore := (peerScore * 0.9) + (medianScore * 0.1)

	return finalScore
}

// Helper function to convert values from map to float64
func parseFloat(value interface{}) float64 {
	switch v := value.(type) {
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0.0
		}
		return f
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0.0
	}
}
func analyzeTrend(stock Stock, pastData interface{}) float64 {
	trendScore := 0.0
	comparisons := 0 // Keep track of the number of comparisons

	// Ensure pastData is in bson.M format
	if data, ok := pastData.(bson.M); ok {
		for _, quarterData := range data {
			// zap.L().Info("Processing quarter", zap.String("quarter", key))

			// Process the quarter data if it's a primitive.A (array of quarter maps)
			if quarterArray, ok := quarterData.(primitive.A); ok {
				var prevElem bson.M
				for i, elem := range quarterArray {
					if elemMap, ok := elem.(bson.M); ok {
						// zap.L().Info("Processing quarter element", zap.Any("element", elemMap))

						// Only perform comparisons starting from the second element
						if i > 0 && prevElem != nil {
							// zap.L().Info("Comparing with previous element", zap.Any("previous", prevElem), zap.Any("current", elemMap))

							// Iterate over the keys in the current quarter and compare with previous quarter
							for key, v := range elemMap {
								if prevVal, ok := prevElem[key]; ok {
									// Compare consecutive values for the same key
									if toFloat(v) > toFloat(prevVal) {
										trendScore += 5
									} else if toFloat(v) < toFloat(prevVal) {
										trendScore -= 5
									}
									// Increment comparisons for each valid comparison
									comparisons++
								}
							}
						}
						// Update previous element for next iteration
						prevElem = elemMap
					}
				}
			}
		}
	}

	// Normalize the score by dividing it by the number of comparisons
	if comparisons > 0 {
		return trendScore / float64(comparisons)
	}
	return 0.0 // Return 0 if no comparisons were made
}

// prosConsAdjustment calculates score adjustments based on pros and cons
func prosConsAdjustment(stock Stock) float64 {
	adjustment := 0.0

	// Adjust score based on pros
	// for _, pro := range stock.Pros {
	// zap.L().Info("Pro", zap.String("pro", pro)) // This line is optional, just showing how we could use 'pro'
	adjustment += toFloat(1.0 * len(stock.Pros))
	// }

	// Adjust score based on cons
	// for _, con := range stock.Cons {
	// zap.L().Info("Con", zap.String("con", con)) // This line is optional, just showing how we could use 'con'
	adjustment -= toFloat(1.0 * len(stock.Cons))
	// }/

	return adjustment
}

// rateStock calculates the final stock rating
func rateStock(stock map[string]interface{}) float64 {
	// zap.L().Info("Stock data", zap.Any("stock", stock))
	stockData := Stock{
		Name:          stock["name"].(string),
		PE:            toFloat(stock["stockPE"]),
		MarketCap:     toFloat(stock["marketCap"]),
		DividendYield: toFloat(stock["dividendYield"]),
		ROCE:          toFloat(stock["roce"]),
		Cons:          toStringArray(stock["cons"]),
		Pros:          toStringArray(stock["pros"]),
	}
	// zap.L().Info("Stock data", zap.Any("stock", stockData))
	// zap.L().Info("Stock data", zap.Any("stock", stockData))
	peerComparisonScore := compareWithPeers(stockData, stock["peers"]) * 0.5
	trendScore := analyzeTrend(stockData, stock["quarterlyResults"]) * 0.4
	// prosConsScore := prosConsAdjustment(stock) * 0.1
	// zap.L().Info("Peer comparison score", zap.Float64("peerComparisonScore", peerComparisonScore))

	finalScore := peerComparisonScore + trendScore
	finalScore = math.Round(finalScore*100) / 100
	return finalScore
}

func calculateRoa(netProfit string, totalAssets string) float64 {
	// Calculate the Return on Assets (ROA) for the current year
	currentYearRoa := toFloat(netProfit) / toFloat(totalAssets)

	return currentYearRoa
}

func increaseInRoa(netProfit primitive.A, totalAssets primitive.A) bool {
	// Calculate the Return on Assets (ROA) for the current year
	currentYearRoa := calculateRoa(netProfit[len(netProfit)-2].(string), totalAssets[len(totalAssets)-1].(string)) // No TTM in the denominator

	// Calculate the Return on Assets (ROA) for the previous year
	previousYearRoa := calculateRoa(netProfit[len(netProfit)-3].(string), totalAssets[len(totalAssets)-2].(string)) // No TTM in the denominator

	return currentYearRoa > previousYearRoa
}

// Helper function to generate the F-Score for a stock
func generateFScore(stock map[string]interface{}) int {
	fScore := 0
	fScore += calculateProfitabilityScore(stock)
	fScore += calculateLeverageScore(stock)
	fScore += calculateOperatingEfficiencyScore(stock)
	return fScore
}

func calculateProfitabilityScore(stock map[string]interface{}) int {
	score := 0

	// 1 - Profitability Ratios
	// 1.1 - Is the ROA (Return on Assets) positive?
	netProfit := getNestedArrayField(stock, "profitLoss", "Net Profit +")
	totalAssets := getNestedArrayField(stock, "balanceSheet", "Total Assets")

	if len(netProfit) > 0 && len(totalAssets) > 0 {
		roa := calculateRoa(netProfit[len(netProfit)-2].(string), totalAssets[len(totalAssets)-1].(string))
		if roa > 0 {
			score++
		}
	}

	// 1.2 - Positive Cash from Operating Activities in the current year compared to the previous year
	cashFlowOps := getNestedArrayField(stock, "cashFlows", "Cash from Operating Activity +")
	if len(cashFlowOps) > 1 {
		currentCashFlow := toFloat(cashFlowOps[len(cashFlowOps)-1])
		previousCashFlow := toFloat(cashFlowOps[len(cashFlowOps)-2])
		if currentCashFlow > previousCashFlow {
			score++
		}
	}

	// 1.3 - Positive Return on Assets in the current year compared to the previous year
	if increaseInRoa(netProfit, totalAssets) {
		score++
	}

	// 1.4 - Higher Cash from Operating Activities than Net Profit (excluding TTM value)
	if len(cashFlowOps) > 0 && len(netProfit) > 1 {
		cashFlow := toFloat(cashFlowOps[len(cashFlowOps)-1])
		profit := toFloat(netProfit[len(netProfit)-2])
		if cashFlow > profit {
			score++
		}
	}

	return score
}

func calculateLeverageScore(stock map[string]interface{}) int {
	score := 0

	// 2 - Leverage, Liquidity, and Source of Funds
	// 2.1 Lower Long-term Debt to Total Assets ratio in the current year compared to the previous year
	borrowings := getNestedArrayField(stock, "balanceSheet", "Borrowings +")
	totalAssets := getNestedArrayField(stock, "balanceSheet", "Total Assets")
	if len(borrowings) > 1 && len(totalAssets) > 1 {
		currentRatio := toFloat(borrowings[len(borrowings)-1]) / toFloat(totalAssets[len(totalAssets)-1])
		previousRatio := toFloat(borrowings[len(borrowings)-2]) / toFloat(totalAssets[len(totalAssets)-2])
		if currentRatio <= previousRatio {
			score++
		}
	}

	// 2.2 Higher Current Ratio in the current year compared to the previous year
	otherAssets := getNestedArrayField(stock, "balanceSheet", "Other Assets +")
	otherLiabilities := getNestedArrayField(stock, "balanceSheet", "Other Liabilities +")
	if len(otherAssets) > 1 && len(otherLiabilities) > 1 {
		currentRatio := toFloat(otherAssets[len(otherAssets)-1]) / toFloat(otherLiabilities[len(otherLiabilities)-1])
		previousRatio := toFloat(otherAssets[len(otherAssets)-2]) / toFloat(otherLiabilities[len(otherLiabilities)-2])
		if currentRatio > previousRatio {
			score++
		}
	}

	// 2.3 No new shares issued in the last year - assuming Equity Capital is the same as Share Capital
	equityCapital := getNestedArrayField(stock, "balanceSheet", "Equity Capital")
	if len(equityCapital) > 1 {
		currentEquity := toFloat(equityCapital[len(equityCapital)-1])
		previousEquity := toFloat(equityCapital[len(equityCapital)-2])
		if currentEquity <= previousEquity {
			score++
		}
	}

	return score
}

func calculateOperatingEfficiencyScore(stock map[string]interface{}) int {
	score := 0

	// 3 - Operating Efficiency
	// 3.1 Higher Gross Margin in the current year compared to the previous year - excluding TTM value
	opm := getNestedArrayField(stock, "profitLoss", "OPM %")
	if len(opm) > 2 {
		currentOpm := toFloat(opm[len(opm)-2])
		previousOpm := toFloat(opm[len(opm)-3])
		if currentOpm > previousOpm {
			score++
		}
	}

	// 3.2 Higher Asset Turnover Ratio in the current year compared to the previous year - excluding TTM value for sales
	sales := getNestedArrayField(stock, "profitLoss", "Sales +")
	totalAssets := getNestedArrayField(stock, "balanceSheet", "Total Assets")
	if len(sales) > 2 && len(totalAssets) > 1 {
		currentRatio := toFloat(sales[len(sales)-2]) / toFloat(totalAssets[len(totalAssets)-1])
		previousRatio := toFloat(sales[len(sales)-3]) / toFloat(totalAssets[len(totalAssets)-2])
		if currentRatio > previousRatio {
			score++
		}
	}

	return score
}

// Helper function to get an array field from a nested map
func getNestedArrayField(stock map[string]interface{}, path ...string) primitive.A {
	var current bson.M = stock

	for i, key := range path {
		key = strings.TrimSpace(key)

		// Replace " +" with a non-breaking space and plus sign
		if strings.Contains(key, "+") {
			key = strings.ReplaceAll(key, " +", "\u00A0+")
		}

		// If we're at the last key in the path
		if i == len(path)-1 {
			if result, ok := current[key].(primitive.A); ok {
				return result
			}
			return primitive.A{}
		}

		// Expect another nested map for intermediate keys
		if result, ok := current[key].(bson.M); ok {
			current = result
		} else {
			return primitive.A{} // Return empty if the next level is not a map
		}
	}

	return primitive.A{} // Return empty in case path traversal is not successful
}

// Helper function to normalize strings
func normalizeString(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Helper function to match header titles
func matchHeader(cellValue string, patterns []string) bool {
	normalizedValue := normalizeString(cellValue)
	for _, pattern := range patterns {
		matched, _ := regexp.MatchString(pattern, normalizedValue)
		if matched {
			return true
		}
	}
	return false
}

var (
	client *mongo.Client
	once   sync.Once
)

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file")
	}
	once.Do(func() {
		serverAPI := options.ServerAPI(options.ServerAPIVersion1)
		mongoURI := os.Getenv("MONGO_URI")
		// zap.L().Info("Mongo URI", zap.String("uri", mongoURI))
		opts := options.Client().ApplyURI(mongoURI).SetServerAPIOptions(serverAPI)
		// Create a new client and connect to the server
		var err error
		client, err = mongo.Connect(context.TODO(), opts)
		if err != nil {
			panic(err)
		}

		// Send a ping to confirm a successful connection
		pingCmd := bson.M{"ping": 1}
		if err := client.Database("admin").RunCommand(context.TODO(), pingCmd).Err(); err != nil {
			panic(err)
		}

		zap.L().Info("Connected to MongoDB")

	})
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, trell-auth-token, trell-app-version-int, creator-space-auth-token")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// GracefulShutdown handles graceful shutdown of the server and ticker
func GracefulShutdown(server *http.Server, ticker *time.Ticker) {
	stopper := make(chan os.Signal, 1)
	// Listen for interrupt and SIGTERM signals
	signal.Notify(stopper, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stopper
		zap.L().Info("Shutting down gracefully...")

		// Stop the ticker
		ticker.Stop()

		// Create a context with a timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Shut down the server
		if err := server.Shutdown(ctx); err != nil {
			zap.L().Error("Server shutdown failed", zap.Error(err))
			return
		}
		zap.L().Info("Server exited gracefully")
	}()
}

func checkInstrumentName(input string) bool {
	// Regular expression to match "Name of the Instrument" or "Name of Instrument"
	pattern := `Name of (the )?Instrument`

	// Compile the regex
	re := regexp.MustCompile(pattern)

	// Check if the pattern matches the input string
	return re.MatchString(input)
}

var (
	// map few values to some in constand string
	mapValues = map[string]string{
		"Sun Pharmaceutical Industries Limited":       "Sun Pharma.Inds.",
		"KEC International Limited":                   "K E C Intl.",
		"Sandhar Technologies Limited":                "Sandhar Tech",
		"Samvardhana Motherson International Limited": "Samvardh. Mothe.",
		"Coromandel International Limited":            "Coromandel Inter",
	}
)

func parseXlsxFile(c *gin.Context) {
	// Parse the form and retrieve the uploaded files
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(400, gin.H{"error": "Error parsing form data"})
		return
	}

	// Retrieve the files from the form
	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(400, gin.H{"error": "No files found"})
		return
	}

	fmt.Printf("Number of files: %d\n", len(files))

	// Initialize Cloudinary
	cld, err := cloudinary.NewFromURL(os.Getenv("CLOUDINARY_URL"))
	if err != nil {
		c.JSON(500, gin.H{"error": "Error initializing Cloudinary"})
		return
	}

	// Set headers for chunked transfer (if needed)
	c.Writer.Header().Set("Content-Type", "text/plain")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")

	// Iterate over the uploaded XLSX files
	for _, fileHeader := range files {
		// Open each file for processing
		file, err := fileHeader.Open()
		if err != nil {
			zap.L().Error("Error opening file", zap.String("filename", fileHeader.Filename), zap.Error(err))
			continue
		}
		defer file.Close()

		// Generate a UUID for the filename
		uuid := uuid.New().String()
		cloudinaryFilename := uuid + ".xlsx"

		// Upload file to Cloudinary
		uploadResult, err := cld.Upload.Upload(c, file, uploader.UploadParams{
			PublicID: cloudinaryFilename,
			Folder:   "xlsx_uploads",
		})
		if err != nil {
			zap.L().Error("Error uploading file to Cloudinary", zap.String("filename", fileHeader.Filename), zap.Error(err))
			continue
		}

		zap.L().Info("File uploaded to Cloudinary", zap.String("filename", fileHeader.Filename), zap.String("url", uploadResult.SecureURL))

		// Create a new reader from the uploaded file
		file.Seek(0, 0) // Reset file pointer to the beginning
		f, err := excelize.OpenReader(file)
		if err != nil {
			zap.L().Error("Error parsing XLSX file", zap.String("filename", fileHeader.Filename), zap.Error(err))
			continue
		}
		defer f.Close()

		// Get all the sheet names
		sheetList := f.GetSheetList()
		// Loop through the sheets and extract relevant information
		for _, sheet := range sheetList {
			zap.L().Info("Processing file", zap.String("filename", fileHeader.Filename), zap.String("sheet", sheet))

			// Get all the rows in the sheet
			rows, err := f.GetRows(sheet)
			if err != nil {
				zap.L().Error("Error reading rows from sheet", zap.String("sheet", sheet), zap.Error(err))
				continue
			}

			headerFound := false
			headerMap := make(map[string]int)
			stopExtracting := false

			// Loop through the rows in the sheet
			for _, row := range rows {
				if len(row) == 0 {
					continue
				}

				if !headerFound {
					for _, cell := range row {
						if matchHeader(cell, []string{`name\s*of\s*(the)?\s*instrument`}) {
							headerFound = true
							// Build the header map
							for i, headerCell := range row {
								normalizedHeader := normalizeString(headerCell)
								// Map possible variations to standard keys
								switch {
								case matchHeader(normalizedHeader, []string{`name\s*of\s*(the)?\s*instrument`}):
									headerMap["Name of the Instrument"] = i
								case matchHeader(normalizedHeader, []string{`isin`}):
									headerMap["ISIN"] = i
								case matchHeader(normalizedHeader, []string{`rating\s*/\s*industry`, `industry\s*/\s*rating`}):
									headerMap["Industry/Rating"] = i
								case matchHeader(normalizedHeader, []string{`quantity`}):
									headerMap["Quantity"] = i
								case matchHeader(normalizedHeader, []string{`market\s*/\s*fair\s*value.*`, `market\s*value.*`}):
									headerMap["Market/Fair Value"] = i
								case matchHeader(normalizedHeader, []string{`%.*nav`, `%.*net\s*assets`}):
									headerMap["Percentage of AUM"] = i
								}
							}
							// zap.L().Info("Header found", zap.Any("headerMap", headerMap))
							break
						}
					}
					continue
				}

				// Check for the end marker "Subtotal" or "Total"
				joinedRow := strings.Join(row, "")
				if strings.Contains(strings.ToLower(joinedRow), "subtotal") || strings.Contains(strings.ToLower(joinedRow), "total") {
					stopExtracting = true
					break
				}

				if !stopExtracting {
					stockDetail := make(map[string]interface{})

					// Extract data using the header map
					for key, idx := range headerMap {
						if idx < len(row) {
							stockDetail[key] = row[idx]
						} else {
							stockDetail[key] = ""
						}
					}

					// Check if the stockDetail has meaningful data
					if stockDetail["Name of the Instrument"] == nil || stockDetail["Name of the Instrument"] == "" {
						continue
					}

					// Additional processing
					instrumentName, ok := stockDetail["Name of the Instrument"].(string)
					if !ok {
						continue
					}

					// Apply mapping if exists
					if mappedName, exists := mapValues[instrumentName]; exists {
						stockDetail["Name of the Instrument"] = mappedName
						instrumentName = mappedName
					}

					// Clean up the query string
					queryString := instrumentName
					queryString = strings.ReplaceAll(queryString, " Corporation ", " Corpn ")
					queryString = strings.ReplaceAll(queryString, " corporation ", " Corpn ")
					queryString = strings.ReplaceAll(queryString, " Limited", " Ltd ")
					queryString = strings.ReplaceAll(queryString, " limited", " Ltd ")
					queryString = strings.ReplaceAll(queryString, " and ", " & ")
					queryString = strings.ReplaceAll(queryString, " And ", " & ")

					// Prepare the text search filter
					textSearchFilter := bson.M{
						"$text": bson.M{
							"$search": queryString,
						},
					}

					// MongoDB collection
					collection := client.Database(os.Getenv("DATABASE")).Collection(os.Getenv("COLLECTION"))

					// Set find options
					findOptions := options.FindOne()
					findOptions.SetProjection(bson.M{
						"score": bson.M{"$meta": "textScore"},
					})
					findOptions.SetSort(bson.M{
						"score": bson.M{"$meta": "textScore"},
					})

					// Perform the search
					var result bson.M
					err = collection.FindOne(context.TODO(), textSearchFilter, findOptions).Decode(&result)
					if err != nil {
						zap.L().Error("Error finding document", zap.Error(err))
						continue
					}

					fmt.Printf("ResultScore: %v ; Name: %v \n", result["score"], result["name"])
					// Process based on the score
					if score, ok := result["score"].(float64); ok {
						//Very high score -  no need to fetch data of the company
						if score >= 1 {
							// zap.L().Info("marketCap", zap.Any("marketCap", result["marketCap"]), zap.Any("name", stockDetail["Name of the Instrument"]))
							stockDetail["marketCapValue"] = result["marketCap"]
							stockDetail["url"] = result["url"]
							stockDetail["marketCap"] = getMarketCapCategory(fmt.Sprintf("%v", result["marketCap"]))
							stockDetail["stockRate"] = rateStock(result)
							stockDetail["f_score"] = generateFScore(result)
						} else { // Score less than 1 - fetch data of the company
							// fmt.Println("score less than 1", score)
							// zap.L().Info("score less than 1", zap.Float64("score", score))
							results, err := searchCompany(instrumentName)
							if err != nil || len(results) == 0 {
								zap.L().Error("No company found", zap.Error(err))
								continue
							}
							data, err := fetchCompanyData(results[0].URL)
							if err != nil {
								zap.L().Error("Error fetching company data", zap.Error(err))
								continue
							}
							// Update MongoDB with fetched data
							update := bson.M{
								"$set": bson.M{
									"marketCap":           data["Market Cap"],
									"currentPrice":        data["Current Price"],
									"highLow":             data["High / Low"],
									"stockPE":             data["Stock P/E"],
									"bookValue":           data["Book Value"],
									"dividendYield":       data["Dividend Yield"],
									"roce":                data["ROCE"],
									"roe":                 data["ROE"],
									"faceValue":           data["Face Value"],
									"pros":                data["pros"],
									"cons":                data["cons"],
									"quarterlyResults":    data["quarterlyResults"],
									"profitLoss":          data["profitLoss"],
									"balanceSheet":        data["balanceSheet"],
									"cashFlows":           data["cashFlows"],
									"ratios":              data["ratios"],
									"shareholdingPattern": data["shareholdingPattern"],
									"peersTable":          data["peersTable"],
									"peers":               data["peers"],
								},
							}
							updateOptions := options.Update().SetUpsert(true)
							filter := bson.M{"name": results[0].Name}
							_, err = collection.UpdateOne(context.TODO(), filter, update, updateOptions)
							if err != nil {
								zap.L().Error("Failed to update document", zap.Error(err))
							} else {
								zap.L().Info("Successfully updated document", zap.String("company", results[0].Name))
							}
						}
					} else {
						zap.L().Error("No score available for", zap.String("company", instrumentName))
					}

					// Marshal and write the stockDetail
					stockDataMarshal, err := json.Marshal(stockDetail)
					if err != nil {
						zap.L().Error("Error marshalling data", zap.Error(err))
						continue
					}

					_, err = c.Writer.Write(append(stockDataMarshal, '\n')) // Send each stockDetail as JSON with a newline separator

					if err != nil {
						zap.L().Error("Error writing data", zap.Error(err))
						break
					}
					c.Writer.Flush() // Flush each chunk immediately
				}
			}
		}
	}
	c.Writer.Write([]byte("\nStream complete.\n"))
	c.Writer.Flush() // Ensure the final response is sent
}

func runningServer(c *gin.Context) {
	c.JSON(200, gin.H{"message": "Server is running"})
}
func toFloat(value interface{}) float64 {
	if str, ok := value.(string); ok {
		// Remove commas from the string
		cleanStr := strings.ReplaceAll(str, ",", "")

		// Check if the string contains a percentage symbol
		if strings.Contains(cleanStr, "%") {
			// Remove the percentage symbol
			cleanStr = strings.ReplaceAll(cleanStr, "%", "")
			// Convert to float and divide by 100 to get the decimal equivalent
			f, err := strconv.ParseFloat(cleanStr, 64)
			if err != nil {
				zap.L().Error("Error converting to float64", zap.Error(err))
				return 0.0
			}
			return f / 100.0
		}

		// Parse the cleaned string to float
		f, err := strconv.ParseFloat(cleanStr, 64)
		if err != nil {
			zap.L().Error("Error converting to float64", zap.Error(err))
			return 0.0
		}
		return f
	}
	return 0.0
}

func toStringArray(value interface{}) []string {
	if arr, ok := value.(primitive.A); ok {
		var strArr []string
		for _, v := range arr {
			if str, ok := v.(string); ok {
				strArr = append(strArr, str)
			}
		}
		return strArr
	}
	return []string{}
}

func getMarketCapCategory(marketCapValue string) string {

	cleanMarketCapValue := strings.ReplaceAll(marketCapValue, ",", "")

	marketCap, err := strconv.ParseFloat(cleanMarketCapValue, 64) // 64-bit float
	if err != nil {
		log.Println("Failed to convert market cap to integer: %v", err)
	}
	// Define market cap categories in crore (or billions as per comment)
	if marketCap >= 20000 {
		return "Large Cap"
	} else if marketCap >= 5000 && marketCap < 20000 {
		return "Mid Cap"
	} else if marketCap < 5000 {
		return "Small Cap"
	}
	return "Unknown Category"
}

func main() {

	log.Println("MONGO_URI:", os.Getenv("MONGO_URI"))
	log.Println("CLOUDINARY_URL:", os.Getenv("CLOUDINARY_URL"))

	ticker := time.NewTicker(48 * time.Second)

	go func() {
		for t := range ticker.C {
			log.Println("Tick at", t)
			cmd := exec.Command("curl", "https://stock-backend-hz83.onrender.com/api/keepServerRunning")
			output, err := cmd.CombinedOutput()
			if err != nil {
				log.Println("Error running curl:", err)
				return
			}

			// Print the output of the curl command
			log.Println("Curl output:", string(output))

		}
	}()

	router := gin.New()
	router.Use(CORSMiddleware())

	v1 := router.Group("/api")

	{
		v1.POST("/uploadXlsx", parseXlsxFile)
		v1.GET("/keepServerRunning", runningServer)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	// Create a server instance using gin engine as handler
	server := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Call GracefulShutdown with the server and ticker
	GracefulShutdown(server, ticker)

	// Start the server
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Error starting server: %v", err)
	}

}

func fetchCompanyData(url string) (map[string]interface{}, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch the URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to retrieve the content, status code: %d", resp.StatusCode)
	}

	// Parse the HTML content of the company page
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the HTML content: %v", err)
	}
	// Extract data-warehouse-id
	companyData := make(map[string]interface{})

	dataWarehouseID, exists := doc.Find("div[data-warehouse-id]").Attr("data-warehouse-id")
	if exists {
		peerData, err := fetchPeerData(dataWarehouseID)
		if err == nil {
			companyData["peers"] = peerData
		}
	}

	// Extract the data we need
	// Extract data as specified
	doc.Find("li.flex.flex-space-between[data-source='default']").Each(func(index int, item *goquery.Selection) {
		key := strings.TrimSpace(item.Find("span.name").Text())

		// Extract value text and clean it up
		value := strings.TrimSpace(item.Find("span.nowrap.value").Text())
		value = strings.ReplaceAll(value, "\n", "") // Remove newlines
		value = strings.ReplaceAll(value, " ", "")  // Remove extra spaces

		// Extract the numeric value if it exists inside the nested span and clean it up
		number := item.Find("span.number").Text()
		if number != "" {
			number = strings.TrimSpace(number)
			value = strings.ReplaceAll(value, number, number) // Ensure no extra spaces around numbers
		}

		// Remove currency symbols and units from value
		value = strings.ReplaceAll(value, "₹", "")
		value = strings.ReplaceAll(value, "Cr.", "")
		value = strings.ReplaceAll(value, "%", "")

		// Add to company data
		companyData[key] = value

		// Print cleaned key-value pairs
		zap.L().Info("Company Data", zap.String("key", key), zap.String("value", value))
		log.Printf("%s: %s\n", key, value)
	})
	// Extract pros
	var pros []string
	doc.Find("div.pros ul li").Each(func(index int, item *goquery.Selection) {
		pro := strings.TrimSpace(item.Text())
		pros = append(pros, pro)
	})
	companyData["pros"] = pros

	// Extract cons
	var cons []string
	doc.Find("div.cons ul li").Each(func(index int, item *goquery.Selection) {
		con := strings.TrimSpace(item.Text())
		cons = append(cons, con)
	})
	companyData["cons"] = cons
	// Extract Quarterly Results
	quarterlyResults := make(map[string][]map[string]string)
	// Get the months (headers) from the table
	var months []string
	doc.Find("table.data-table thead tr th").Each(func(index int, item *goquery.Selection) {
		month := strings.TrimSpace(item.Text())
		if month != "" && month != "-" { // Skip empty or irrelevant headers
			months = append(months, month)
		}
	})

	// Iterate over each row in the tbody
	doc.Find("table.data-table tbody tr").Each(func(index int, row *goquery.Selection) {
		fieldName := strings.TrimSpace(row.Find("td.text").Text())
		var fieldData []map[string]string

		// Iterate over each column in the row
		row.Find("td").Each(func(colIndex int, col *goquery.Selection) {
			if colIndex > 0 && colIndex <= len(months) { // Ensure we are within the bounds of the months array
				value := strings.TrimSpace(col.Text())
				month := months[colIndex]
				fieldData = append(fieldData, map[string]string{
					month: value,
				})
			}
		})

		if len(fieldData) > 0 {
			quarterlyResults[fieldName] = fieldData
		}
	})

	companyData["quarterlyResults"] = quarterlyResults
	profitLossSection := doc.Find("section#profit-loss")
	if profitLossSection.Length() > 0 {
		companyData["profitLoss"] = parseTableData(profitLossSection, "div[data-result-table]")
	}
	balanceSheetSection := doc.Find("section#balance-sheet")
	if balanceSheetSection.Length() > 0 {
		companyData["balanceSheet"] = parseTableData(balanceSheetSection, "div[data-result-table]")
	}
	shareHoldingPattern := doc.Find("section#shareholding")
	if shareHoldingPattern.Length() > 0 {
		companyData["shareholdingPattern"] = parseShareholdingPattern(shareHoldingPattern)
	}

	ratiosSection := doc.Find("section#ratios")
	if ratiosSection.Length() > 0 {
		companyData["ratios"] = parseTableData(ratiosSection, "div[data-result-table]")
	}
	cashFlowsSection := doc.Find("section#cash-flow")
	if cashFlowsSection.Length() > 0 {
		companyData["cashFlows"] = parseTableData(cashFlowsSection, "div[data-result-table]")
	}
	return companyData, nil
}

func parsePeersTable(doc *goquery.Document, selector string) []map[string]string {
	var peers []map[string]string
	headers := []string{}

	// Extract table headers
	doc.Find(fmt.Sprintf("%s table thead tr th", selector)).Each(func(i int, s *goquery.Selection) {
		headers = append(headers, strings.TrimSpace(s.Text()))
	})

	// Parse each row of the peers table
	doc.Find(fmt.Sprintf("%s table tbody tr", selector)).Each(func(i int, row *goquery.Selection) {
		peerData := map[string]string{}
		row.Find("td").Each(func(j int, cell *goquery.Selection) {
			if j < len(headers) {
				peerData[headers[j]] = strings.TrimSpace(cell.Text())
			}
		})
		peers = append(peers, peerData)
	})

	return peers
}

func fetchPeerData(dataWarehouseID string) ([]map[string]string, error) {
	time.Sleep(1 * time.Second)
	peerURL := fmt.Sprintf(os.Getenv("COMPANY_URL")+"/api/company/%s/peers/", dataWarehouseID)

	// Create a new HTTP request
	req, err := http.NewRequest("GET", peerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request to peers API: %w", err)
	}

	// Add any required headers or cookies here
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching peers data from API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		bodyString := string(bodyBytes)
		zap.L().Error("Received non-200 response code", zap.Int("status_code", resp.StatusCode), zap.String("body", bodyString))
		return nil, fmt.Errorf("received non-200 response code from peers API: %d", resp.StatusCode)
	}

	// Parse the HTML response
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error parsing HTML response: %w", err)
	}

	var peersData []map[string]string
	var medianData map[string]string

	// Parse peers data from the table rows
	doc.Find("tr[data-row-company-id]").Each(func(index int, item *goquery.Selection) {
		peer := make(map[string]string)

		peer["name"] = item.Find("td.text a").Text()
		peer["current_price"] = strings.TrimSpace(item.Find("td").Eq(2).Text())
		peer["pe"] = strings.TrimSpace(item.Find("td").Eq(3).Text())
		peer["market_cap"] = strings.TrimSpace(item.Find("td").Eq(4).Text())
		peer["div_yield"] = strings.TrimSpace(item.Find("td").Eq(5).Text())
		peer["np_qtr"] = strings.TrimSpace(item.Find("td").Eq(6).Text())
		peer["qtr_profit_var"] = strings.TrimSpace(item.Find("td").Eq(7).Text())
		peer["sales_qtr"] = strings.TrimSpace(item.Find("td").Eq(8).Text())
		peer["qtr_sales_var"] = strings.TrimSpace(item.Find("td").Eq(9).Text())
		peer["roce"] = strings.TrimSpace(item.Find("td").Eq(10).Text())

		peersData = append(peersData, peer)
	})

	// Parse median data from the footer of the table
	doc.Find("tfoot tr").Each(func(index int, item *goquery.Selection) {
		medianData = make(map[string]string)
		medianData["company_count"] = strings.TrimSpace(item.Find("td").Eq(1).Text())
		medianData["current_price"] = strings.TrimSpace(item.Find("td").Eq(2).Text())
		medianData["pe"] = strings.TrimSpace(item.Find("td").Eq(3).Text())
		medianData["market_cap"] = strings.TrimSpace(item.Find("td").Eq(4).Text())
		medianData["div_yield"] = strings.TrimSpace(item.Find("td").Eq(5).Text())
		medianData["np_qtr"] = strings.TrimSpace(item.Find("td").Eq(6).Text())
		medianData["qtr_profit_var"] = strings.TrimSpace(item.Find("td").Eq(7).Text())
		medianData["sales_qtr"] = strings.TrimSpace(item.Find("td").Eq(8).Text())
		medianData["qtr_sales_var"] = strings.TrimSpace(item.Find("td").Eq(9).Text())
		medianData["roce"] = strings.TrimSpace(item.Find("td").Eq(10).Text())
	})

	peersData = append(peersData, medianData)
	return peersData, nil
}

type Company struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func searchCompany(queryString string) ([]Company, error) {
	// Replace "corporation" with "Corpn" and "limited" with "Ltd"
	queryString = strings.ReplaceAll(queryString, " Corporation ", " Corpn ")
	queryString = strings.ReplaceAll(queryString, " corporation ", " Corpn ")
	queryString = strings.ReplaceAll(queryString, " Limited", " Ltd ")
	queryString = strings.ReplaceAll(queryString, " limited", " Ltd ")
	queryString = strings.ReplaceAll(queryString, " and ", " & ")
	queryString = strings.ReplaceAll(queryString, " And ", " & ")
	// Base URL for the Screener API
	baseURL := os.Getenv("COMPANY_URL") + "/api/company/search/"

	// Create the URL with query parameters
	params := url.Values{}
	params.Add("q", queryString)
	params.Add("v", "3")
	params.Add("fts", "1")

	// Create the request
	req, err := http.NewRequest("GET", baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}

	// Create the client and send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the response
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var searchResponse []Company
	err = json.Unmarshal(body, &searchResponse)
	if err != nil {
		zap.L().Error("Failed to unmarshal search response", zap.Error(err))
		return nil, err
	}

	// Return the list of results
	return searchResponse, nil
}

func parseTableData(section *goquery.Selection, tableSelector string) map[string]interface{} {
	table := section.Find(tableSelector)
	if table.Length() == 0 {
		return nil
	}

	// Extract months/years from table headers
	headers := []string{}
	table.Find("thead th").Each(func(i int, th *goquery.Selection) {
		headers = append(headers, strings.TrimSpace(th.Text()))
	})

	// Extract table rows and values
	data := make(map[string]interface{})
	table.Find("tbody tr").Each(func(i int, tr *goquery.Selection) {
		rowKey := strings.TrimSpace(tr.Find("td.text").Text())
		rowValues := []string{}
		tr.Find("td").Each(func(i int, td *goquery.Selection) {
			if i > 0 { // Skip the first column which is the row key
				rowValues = append(rowValues, strings.TrimSpace(td.Text()))
			}
		})
		data[rowKey] = rowValues
	})

	return data
}

func parseShareholdingPattern(section *goquery.Selection) map[string]interface{} {
	shareholdingData := make(map[string]interface{})

	// Extract quarterly data
	quarterlyData := parseTable(section.Find("div#quarterly-shp"))
	if len(quarterlyData) > 0 {
		shareholdingData["quarterly"] = quarterlyData
	}

	// Extract yearly data
	yearlyData := parseTable(section.Find("div#yearly-shp"))
	if len(yearlyData) > 0 {
		shareholdingData["yearly"] = yearlyData
	}

	return shareholdingData
}

func parseTable(tableDiv *goquery.Selection) []map[string]interface{} {
	var tableData []map[string]interface{}

	// Get the headers (dates) from the table
	var headers []string
	tableDiv.Find("table thead th").Each(func(index int, header *goquery.Selection) {
		if index > 0 { // Skip the first column header (e.g., "Promoters", "FIIs", etc.)
			headers = append(headers, strings.TrimSpace(header.Text()))
		}
	})

	// Iterate over each row in the table body
	tableDiv.Find("table tbody tr").Each(func(index int, row *goquery.Selection) {
		rowData := make(map[string]interface{})

		// Extract the row label (e.g., "Promoters", "FIIs", etc.)
		label := strings.TrimSpace(row.Find("td.text").Text())
		rowData["category"] = label

		// Extract values for each date (column)
		values := make(map[string]string)
		row.Find("td").Each(func(i int, cell *goquery.Selection) {
			if i > 0 && i <= len(headers) { // Ensure we are within the bounds of the headers array
				date := headers[i-1] // Corresponding date (column header)
				values[date] = strings.TrimSpace(cell.Text())
			}
		})

		rowData["values"] = values
		tableData = append(tableData, rowData)
	})

	return tableData
}
