package iri

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	db "trano/internal/db/sqlc"

	"github.com/PuerkitoBio/goquery"
	"github.com/imroc/req/v3"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type Client struct {
	limiter    *rate.Limiter
	httpClient *http.Client
}

func NewClient(limiter *rate.Limiter, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		limiter:    limiter,
		httpClient: httpClient,
	}
}

type TrainData struct {
	TrainNo          int64
	TrainName        string
	TrainType        string
	Zone             string
	ReturnTrainNo    int64
	CoachComposition string
	SourceURL        string
}

type StationData struct {
	StationCode       string
	StationName       string
	Zone              string
	Division          *string
	Address           string
	ElevationM        *float64
	Lat               *float64
	Lng               *float64
	NumberOfPlatforms *int
	StationType       *string
	StationCategory   *string
	TrackType         *string
}

type ScheduleData struct {
	ScheduleID            int64
	TrainNo               int64
	OriginStationCode     string
	TerminusStationCode   string
	OriginSchDepartureMin int
	TotalDistanceKm       float64
	TotalRuntimeMin       int
	RunningDaysBitmap     int
	Route                 []RouteData
}

type RouteData struct {
	ScheduleID               int64
	StationCode              string
	DistanceKm               float64
	SchArrivalMinFromStart   int
	SchDepartureMinFromStart int
	Stops                    int
}

func (c *Client) FetchTrainData(
	ctx context.Context,
	targetURL string,
) (*TrainData, []*StationData, *ScheduleData, error) {

	// Rate limiting
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, nil, nil, err
		}
	}

	// Single persistent client (cookies, headers, TLS fingerprint stay consistent)
	client := req.C().
		SetTimeout(30 * time.Second)

	// Establish session
	// homeResp, err := client.R().
	// 	SetHeaders(map[string]string{
	// 		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
	// 		"Accept-Encoding":           "gzip, deflate, br, zstd",
	// 		"Accept-Language":           "en-US,en;q=0.9",
	// 		"Cache-Control":             "no-cache",
	// 		"Pragma":                    "no-cache",
	// 		"Priority":                  "u=0, i",
	// 		"Sec-Ch-Ua":                 `"Google Chrome";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
	// 		"Sec-Ch-Ua-Mobile":          "?0",
	// 		"Sec-Ch-Ua-Platform":        `"Windows"`,
	// 		"Sec-Fetch-Dest":            "document",
	// 		"Sec-Fetch-Mode":            "navigate",
	// 		"Sec-Fetch-Site":            "none",
	// 		"Sec-Fetch-User":            "?1",
	// 		"Upgrade-Insecure-Requests": "1",
	// 		"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
	// 	}).
	// 	Get("https://indiarailinfo.com/")
	// if err != nil {
	// 	return nil, nil, nil, err
	// }
	// homeResp.Body.Close()
	// time.Sleep(time.Duration(500+rand.Intn(500)) * time.Millisecond)

	// // First page request
	// resp, err := client.R().
	// 	SetContext(ctx).
	// 	SetHeaders(map[string]string{
	// 		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
	// 		"Accept-Encoding":           "gzip, deflate, br, zstd",
	// 		"Accept-Language":           "en-US,en;q=0.9",
	// 		"Cache-Control":             "no-cache",
	// 		"Pragma":                    "no-cache",
	// 		"Priority":                  "u=0, i",
	// 		"Sec-Ch-Ua":                 `"Google Chrome";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
	// 		"Sec-Ch-Ua-Mobile":          "?0",
	// 		"Sec-Ch-Ua-Platform":        `"Windows"`,
	// 		"Sec-Fetch-Dest":            "document",
	// 		"Sec-Fetch-Mode":            "navigate",
	// 		"Sec-Fetch-Site":            "cross-site",
	// 		"Sec-Fetch-User":            "?1",
	// 		"Upgrade-Insecure-Requests": "1",
	// 		"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
	// 	}).
	// 	Get(targetURL)
	// if err != nil {
	// 	return nil, nil, nil, fmt.Errorf("initial request failed: %w", err)
	// }
	// defer resp.Body.Close()

	// if resp.StatusCode != http.StatusOK {
	// 	return nil, nil, nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	// }

	// docMain, err := goquery.NewDocumentFromReader(resp.Body)
	// if err != nil {
	// 	return nil, nil, nil, fmt.Errorf("html parse failed: %w", err)
	// }

	// // Find timetable link
	// timetableHref, exists := docMain.Find("a[href*='/train/timetable/all/']").Attr("href")
	// if !exists {
	// 	return nil, nil, nil, fmt.Errorf("timetable link not found")
	// }

	// baseURL, err := url_pkg.Parse(targetURL)
	// if err != nil {
	// 	return nil, nil, nil, err
	// }

	// timetableURL := baseURL.ResolveReference(&url_pkg.URL{
	// 	Path: timetableHref,
	// }).String()

	// time.Sleep(time.Duration(700+rand.Intn(1300)) * time.Millisecond)

	// Check if targetURL is valid and points to a train page
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		return nil, nil, nil, fmt.Errorf("targetURL must start with http:// or https://: %s", targetURL)
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid targetURL: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "train" {
		return nil, nil, nil, fmt.Errorf("unexpected targetURL path (should be /train/number): %s", parsed.Path)
	}
	identifier := parts[len(parts)-1]
	timetableURL := fmt.Sprintf("%s://%s/train/timetable/all/%s", parsed.Scheme, parsed.Host, identifier)

	// log.Println(timetableURL)
	//
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, nil, nil, err
		}
	}

	// Timetable page request
	resp, err := client.R().
		SetHeaders(map[string]string{
			"Accept": "text/html",
			// "Accept-Encoding":           "gzip, deflate, br, zstd",
			"Accept-Language": "en-US,en;q=0.9",
			"Cache-Control":   "no-cache",
			"Pragma":          "no-cache",
			// "Priority":                  "u=0, i",
			// "Referer":                   targetURL,
			// "Sec-Ch-Ua":                 `"Google Chrome";v="143", "Chromium";v="143", "Not A(Brand";v="24"`,
			// "Sec-Ch-Ua-Mobile":          "?0",
			// "Sec-Ch-Ua-Platform":        `"Windows"`,
			// "Sec-Fetch-Dest":            "document",
			// "Sec-Fetch-Mode":            "navigate",
			// "Sec-Fetch-Site":            "same-origin",
			// "Sec-Fetch-User":            "?1",
			// "Upgrade-Insecure-Requests": "1",
			"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
		}).
		Get(timetableURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("timetable request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, nil, fmt.Errorf("timetable unexpected status %d", resp.StatusCode)
	}

	// Save the response body to a file
	// bodyBytes, err := io.ReadAll(resp.Body)
	// if err != nil {
	// 	return nil, nil, nil, fmt.Errorf("failed to read response body: %w", err)
	// }
	// if err := os.WriteFile(identifier+".html", bodyBytes, 0644); err != nil {
	// 	return nil, nil, nil, fmt.Errorf("failed to write file: %w", err)
	// }

	docTimetable, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("timetable html parse failed: %w", err)
	}

	trainData, stationData, scheduleData, err := c.parseTrainData(docTimetable, targetURL)
	if err != nil {
		return nil, nil, nil, err
	}

	return trainData, stationData, scheduleData, nil
}

func (c *Client) parseTrainData(doc *goquery.Document, sourceURL string) (*TrainData, []*StationData, *ScheduleData, error) {
	trainData := &TrainData{SourceURL: sourceURL}
	stationData := []*StationData{}
	scheduleData := &ScheduleData{}

	// Train No and Name from h1
	divText := doc.Find("h1").First().
		Children().First().
		Children().First().
		Children().First().
		Text()
	if strings.Contains(divText, "/") {
		parts := strings.SplitN(divText, "/", 2)
		trainNoStr := strings.TrimSpace(parts[0])
		// If contains ⇒, take the part before it as the main train number
		if strings.Contains(trainNoStr, "⇒") {
			trainNoParts := strings.Split(trainNoStr, "⇒")
			trainNoStr = strings.TrimSpace(trainNoParts[0])
		}
		// Remove any trailing non-digit characters (e.g., X, A, etc.)
		re := regexp.MustCompile(`^(\d+)`)
		if m := re.FindStringSubmatch(trainNoStr); len(m) > 1 {
			trainNoStr = m[1]
		}
		if trainNoStr != "" {
			if no, err := strconv.ParseInt(trainNoStr, 10, 64); err == nil {
				trainData.TrainNo = no
				scheduleData.TrainNo = no
			}
		}
		if len(parts) > 1 {
			namePart := strings.TrimSpace(parts[1])
			// Drop trailing bracketed or non-latin parentheses
			if idx := strings.Index(namePart, "("); idx > 0 {
				namePart = strings.TrimSpace(namePart[:idx])
			}
			trainData.TrainName = namePart
		}
	}

	if trainData.TrainNo == 0 || trainData.TrainName == "" {
		return nil, nil, nil, fmt.Errorf("insufficient train data: trainNo=%d, trainName=%q", trainData.TrainNo, trainData.TrainName)
	}

	// Return Train No
	// find a link with "Return# <number>"
	doc.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		text := a.Text()
		re := regexp.MustCompile(`Return#\s*(\d+)`)
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			if no, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				trainData.ReturnTrainNo = no
				return false
			}
		}
		return true
	})

	// Type, Rake Zone
	var trainType string
	var rakeZone string
	doc.Find("td").EachWithBreak(func(_ int, td *goquery.Selection) bool {
		found := false
		td.Find("div").Each(func(_ int, div *goquery.Selection) {
			html, _ := div.Html()
			text := div.Text()
			if strings.Contains(html, "Type:") {
				span := div.Find("span")
				if span.Length() > 0 {
					trainType = strings.TrimSpace(span.Text())
					found = true
				}
			} else if strings.Contains(html, "Rake Zone:") {
				span := div.Find("span")
				if span.Length() > 0 {
					rakeZone = strings.TrimSpace(span.Text())
					found = true
				} else {
					// fallback: try to extract from text if no span
					zoneRe := regexp.MustCompile(`Rake Zone:\s*([^\n/]+)`)
					if m := zoneRe.FindStringSubmatch(text); len(m) > 1 {
						rakeZone = strings.TrimSpace(m[1])
						found = true
					}
				}
			}
		})
		return !found
	})
	if trainType != "" {
		trainData.TrainType = trainType
	}
	if rakeZone != "" {
		trainData.Zone = rakeZone
	} else {
		pageText := doc.Text()
		if zoneRe := regexp.MustCompile(`Rake Zone:\s*([^/\n]+)`); zoneRe.MatchString(pageText) {
			if m := zoneRe.FindStringSubmatch(pageText); len(m) > 1 {
				zone := strings.TrimSpace(m[1])
				trainData.Zone = zone
			}
		}
	}

	// Coach composition
	doc.Find("div.rake").Each(func(_ int, rakeDiv *goquery.Selection) {
		coaches := make([]string, 0)
		rakeDiv.Children().Each(func(_ int, coachDiv *goquery.Selection) {
			numDiv := coachDiv.Find("div.num")
			if numDiv.Length() > 0 {
				coach := strings.TrimSpace(numDiv.Text())
				if coach != "" {
					coaches = append(coaches, coach)
				}
			}
		})
		if len(coaches) > 0 {
			comp := strings.Join(coaches, ",")
			trainData.CoachComposition = comp
		}
	})

	// Running days bitmap
	var runningDaysBitmap int
	doc.Find("table.deparrgrid").EachWithBreak(func(_ int, table *goquery.Selection) bool {
		row := table.Find("tr").First()
		if row.Length() == 0 {
			return true
		}
		// Each cell represents a day: S M T W T F S
		row.Find("td").Each(func(i int, td *goquery.Selection) {
			// The day order is: S M T W T F S (Sun=0, Mon=1, ..., Sat=6)
			// If the cell has style "opacity:0.2", it's not running that day
			style, _ := td.Attr("style")
			if strings.Contains(style, "opacity:0.2") {
				return
			}
			// If the cell is not faded, set the corresponding bit
			runningDaysBitmap |= 1 << i
		})
		return false
	})
	scheduleData.RunningDaysBitmap = runningDaysBitmap

	// Schedule table parsing
	var routeEntries []RouteData
	originDepTimeMin := -1

	scheduleTable := doc.Find("div.newschtable")
	if scheduleTable.Length() > 0 {
		scheduleTable.Children().Each(func(_ int, row *goquery.Selection) {

			// If a <div> has class="", then row.Attr("class") exists and is the empty string.
			// If a <div> has no class attribute at all, then row.Attr("class") returns ("", false).
			// _, hasClass := row.Attr("class")
			// if !hasClass {
			// 	return
			// }

			cols := row.Children()
			colCount := cols.Length()
			if colCount < 14 {
				return
			}

			colVals := make([]string, colCount)
			cols.Each(func(j int, col *goquery.Selection) {
				colVals[j] = strings.TrimSpace(col.Text())
			})

			// Skip header
			if colVals[2] == "Code" {
				return
			}

			stationCode := colVals[2]
			if stationCode == "" {
				return
			}
			// fmt.Printf("Processing station: %s\n", stationCode)

			stationName := colVals[3]
			arrTime := colVals[6]
			depTime := colVals[8]
			dayStr := colVals[12]
			kmStr := colVals[13]

			day, _ := strconv.Atoi(dayStr)
			if day == 0 {
				day = 1
			}

			distKm, _ := strconv.ParseFloat(kmStr, 64)

			arrMinFromMidnight := parseTime(arrTime)
			depMinFromMidnight := parseTime(depTime)

			// First station (origin), track origin departure time
			if len(routeEntries) == 0 && depMinFromMidnight >= 0 {
				originDepTimeMin = depMinFromMidnight
			}

			isOrigin := (arrTime == "" || arrTime == "-")
			isTerminus := (depTime == "" || depTime == "-")

			// Arrival/departure minutes from origin departure
			arrMinFromStart := -1
			depMinFromStart := -1

			if isOrigin {
				arrMinFromStart = 0
			} else if arrMinFromMidnight >= 0 && originDepTimeMin >= 0 {
				arrMinFromStart = (day-1)*24*60 + arrMinFromMidnight - originDepTimeMin
			}

			if isTerminus {
				depMinFromStart = arrMinFromStart
			} else if depMinFromMidnight >= 0 && originDepTimeMin >= 0 {
				depMinFromStart = (day-1)*24*60 + depMinFromMidnight - originDepTimeMin
			}

			// Stop/pass determination
			stops := 1
			if row.HasClass("brownColor") {
				stops = 0
			}

			routeEntries = append(routeEntries, RouteData{
				StationCode:              stationCode,
				DistanceKm:               distKm,
				SchArrivalMinFromStart:   arrMinFromStart,
				SchDepartureMinFromStart: depMinFromStart,
				Stops:                    stops,
			})

			// Collect station details (zone/address/elevation) if present
			var stationZone, stationAddress, elevationStr string
			if colCount > 16 {
				stationZone = colVals[16]
			}
			if colCount > 17 {
				stationAddress = colVals[17]
			}
			if colCount > 15 {
				elevationStr = colVals[15]
			}

			var elevationM *float64
			if elevationStr != "" && elevationStr != "-" {
				elevStr := strings.TrimSuffix(elevationStr, "m")
				if elev, err := strconv.ParseFloat(elevStr, 64); err == nil {
					elevationM = &elev
				}
			}

			// Parse additional details from tooltip
			stationLink := cols.Eq(2).Find("a")
			title1, hasTitle := stationLink.Attr("title1")
			if !hasTitle {
				title1, hasTitle = stationLink.Attr("title")
				if !hasTitle {
					fmt.Printf("No title1 or title for station %s\n", stationCode)
					return
				}
			}

			division, stationType, category, trackType, numPlatforms := parseStationDetails(title1)

			stationData = append(stationData, &StationData{
				StationCode:       stationCode,
				StationName:       stationName,
				Zone:              stationZone,
				Division:          division,
				Address:           stationAddress,
				ElevationM:        elevationM,
				Lat:               nil, // Not available in HTML
				Lng:               nil, // Not available in HTML
				NumberOfPlatforms: numPlatforms,
				StationType:       stationType,
				StationCategory:   category,
				TrackType:         trackType,
			})
		})
	}

	// 4. Schedule origin/terminus and aggregates
	if len(routeEntries) >= 2 {
		scheduleData.OriginStationCode = routeEntries[0].StationCode
		scheduleData.TerminusStationCode = routeEntries[len(routeEntries)-1].StationCode
		scheduleData.OriginSchDepartureMin = originDepTimeMin
		scheduleData.Route = routeEntries
		scheduleData.TotalDistanceKm = routeEntries[len(routeEntries)-1].DistanceKm

		last := routeEntries[len(routeEntries)-1]
		if last.SchArrivalMinFromStart >= 0 {
			scheduleData.TotalRuntimeMin = last.SchArrivalMinFromStart
		}
	}

	// Basic validation
	if len(routeEntries) < 2 {
		return nil, nil, nil, fmt.Errorf("insufficient route data: routes=%d", len(routeEntries))
	}

	return trainData, stationData, scheduleData, nil
}

// parseTime parses time strings like "10:30" to minutes from midnight. Invalid => -1.
func parseTime(timeStr string) int {
	if timeStr == "" || timeStr == "-" {
		return -1
	}
	parts := strings.Split(timeStr, ":")
	if len(parts) != 2 {
		return -1
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return -1
	}
	return h*60 + m
}

func parseStationDetails(title1 string) (division, stationType, category, trackType *string, numPlatforms *int) {
	if title1 == "" {
		return
	}
	lines := strings.Split(title1, "<br />")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Division: "); ok {
			val := strings.TrimSpace(after)
			if val != "" && val != "n/a" {
				division = &val
			}
		} else if after0, ok0 := strings.CutPrefix(line, "Type: "); ok0 {
			val := strings.TrimSpace(after0)
			if val != "" && val != "n/a" {
				stationType = &val
			}
		} else if after1, ok1 := strings.CutPrefix(line, "Category: "); ok1 {
			val := strings.TrimSpace(after1)
			if val != "" && val != "n/a" {
				category = &val
			}
		} else if after2, ok2 := strings.CutPrefix(line, "Track: "); ok2 {
			val := strings.TrimSpace(after2)
			if val != "" && val != "n/a" {
				trackType = &val
			}
		}
	}
	// Extract number of platforms from the first line, e.g., "SDAH/Sealdah (21 PFs)"
	if len(lines) > 0 {
		firstLine := lines[0]
		re := regexp.MustCompile(`\((\d+) PFs?\)`)
		if m := re.FindStringSubmatch(firstLine); len(m) > 1 {
			if pfs, err := strconv.Atoi(m[1]); err == nil {
				numPlatforms = &pfs
			}
		}
	}
	return
}

func (c *Client) ExecuteSyncCycle(ctx context.Context, dbConn *sql.DB, logger *log.Logger, concurrency int, urls []string) error {
	queries := db.New(dbConn)
	saver := NewSaver(queries, logger)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for _, url := range urls {
		url := url
		g.Go(func() error {
			train, stations, schedule, err := c.FetchTrainData(gctx, url)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					logger.Printf("failed to fetch %s : %v", url, err)
				}
				return nil
				// return err
			}
			// logger.Println("Got the data yey", url, train.TrainName, len(stations))
			if err := saver.SaveTrainData(gctx, train); err != nil {
				logger.Printf("failed to save train %s: %v", url, err)
				return err
			}
			for _, station := range stations {
				if err := saver.SaveStationData(gctx, station); err != nil {
					logger.Printf("failed to save station %s: %v", station.StationCode, err)
					return err
				}
			}
			if err := saver.SaveScheduleData(gctx, schedule); err != nil {
				logger.Printf("failed to save schedule %s: %v", url, err)
				logger.Printf("Schedule Details:\n")
				logger.Printf("  ID: %d\n", schedule.ScheduleID)
				logger.Printf("  Train No: %d\n", schedule.TrainNo)
				logger.Printf("  Origin: %s\n", schedule.OriginStationCode)
				logger.Printf("  Terminus: %s\n", schedule.TerminusStationCode)
				logger.Printf("  Origin Departure: %d min\n", schedule.OriginSchDepartureMin)
				logger.Printf("  Total Distance: %.2f km\n", schedule.TotalDistanceKm)
				logger.Printf("  Total Runtime: %d min\n", schedule.TotalRuntimeMin)
				logger.Printf("  Running Days Bitmap: %d\n", schedule.RunningDaysBitmap)
				logger.Printf("  Number of Route Stops: %d\n", len(schedule.Route))
				logger.Printf("Routes:\n")
				for i, route := range schedule.Route {
					logger.Printf("  %d. Station: %s, Distance: %.2f km, Arr: %d min, Dep: %d min, Stops: %d\n",
						i+1, route.StationCode, route.DistanceKm, route.SchArrivalMinFromStart, route.SchDepartureMinFromStart, route.Stops)
				}
				return err
			}
			logger.Println("Processed ", url)
			return nil
		})
	}
	return g.Wait()
}
