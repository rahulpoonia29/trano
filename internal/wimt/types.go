package wimt

type APIResponse struct {
	Departed              bool          `json:"departed"`
	Qid                   string        `json:"qid"`
	NtesLastUpdated       string        `json:"ntes_lastUpdated"`
	RuntimeInMins         int           `json:"runtime_in_mins"`
	Pop                   int           `json:"pop"`
	Lat                   *float64      `json:"lat"`
	Lng                   *float64      `json:"lng"`
	Classes               []string      `json:"classes"`
	QueryUser             string        `json:"query_user"`
	QueryTime             string        `json:"query_time"`
	Strength              int           `json:"strength"`
	RunningStatus         string        `json:"running_status"`
	DestinationStation    string        `json:"destination_station"`
	CurStn                string        `json:"curStn"`
	Delay                 float64       `json:"delay"`
	RunningStatusAlt      string        `json:"running status"`
	DepartedCurStn        bool          `json:"departedCurStn"`
	StartDate             string        `json:"start_date"`
	TrainName             string        `json:"train_name"`
	StartTime             string        `json:"start_time"`
	LastUpdated           string        `json:"lastUpdated"`
	NtesLastUpdateIsoDate string        `json:"ntes_lastUpdateIsoDate"`
	SourceStation         string        `json:"source_station"`
	GTFSTrainType         string        `json:"gtfs_train_type"`
	Distance              float64       `json:"distance"`
	CurStnDistance        float64       `json:"curStnDistance"`
	Eta                   bool          `json:"eta"`
	LastUpdateIsoDate     string        `json:"lastUpdateIsoDate"`
	WUUID                 string        `json:"w_uuid"`
	Cinfo                 string        `json:"cinfo"`
	TrainType             string        `json:"train_type"`
	PitstopNextToCurstn   Pitstop       `json:"pitstop_next_to_curstn"`
	LastStation           string        `json:"last_station"`
	DaysSchedule          []DaySchedule `json:"days_schedule"`
	NtesStatus            any           `json:"ntes status"`
}

type DaySchedule struct {
	Distance            float64        `json:"distance"`
	PlatformInfo        []PlatformInfo `json:"platform_info"`
	ActualArrivalTime   string         `json:"actual_arrival_time"`
	SchArrivalTime      string         `json:"sch_arrival_time"`
	Sno                 int            `json:"sno"`
	SchDepartureTm      int64          `json:"sch_departure_tm"`
	StationCode         string         `json:"station_code"`
	UpdatedAt           string         `json:"updated_at"`
	Stops               bool           `json:"stops"`
	GTFSSno             int            `json:"gtfs_sno"`
	ActualArrivalDate   string         `json:"actual_arrival_date"`
	SchArrivalTm        int64          `json:"sch_arrival_tm"`
	SchDepartureDate    string         `json:"sch_departure_date"`
	Platform            string         `json:"platform"`
	Lat                 float64        `json:"lat"`
	SchDepartureTime    string         `json:"sch_departure_time"`
	ActualArrivalTm     int64          `json:"actual_arrival_tm"`
	Lng                 float64        `json:"lng"`
	SchArrivalDate      string         `json:"sch_arrival_date"`
	Departed            *bool          `json:"departed,omitempty"`
	ActualDepartureTime string         `json:"actual_departure_time,omitempty"`
	DelayInDeparture    int64          `json:"delay_in_departure,omitempty"`
	ActualDepartureTm   int64          `json:"actual_departure_tm,omitempty"`
	ActualDepartureDate string         `json:"actual_departure_date,omitempty"`
	DelayInArrival      int64          `json:"delay_in_arrival,omitempty"`
	CurStn              *bool          `json:"curStn,omitempty"`
	NonAggDepartureTm   *int64         `json:"non_agg_departure_tm,omitempty"`
	NonAggDepartureDate string         `json:"non_agg_departure_date,omitempty"`
	NonAggDepartureTime string         `json:"non_agg_departure_time,omitempty"`
	NonAggArrivalTm     *int64         `json:"non_agg_arrival_tm,omitempty"`
	NonAggArrivalDate   string         `json:"non_agg_arrival_date,omitempty"`
	NonAggArrivalTime   string         `json:"non_agg_arrival_time,omitempty"`
	Source              string         `json:"source,omitempty"`
}

type Pitstop struct {
	Sno         int    `json:"sno"`
	StationCode string `json:"station_code"`
}
type PlatformInfo struct {
	PlatformNumber string  `json:"platform_number"`
	FeedbackCount  *int    `json:"feedback_count,omitempty"`
	Percentage     *string `json:"percentage,omitempty"`
}
