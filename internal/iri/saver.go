package iri

import (
	"context"
	"database/sql"
	"log"

	db "trano/internal/db/sqlc"
)

type Saver struct {
	queries *db.Queries
	logger  *log.Logger
}

func NewSaver(queries *db.Queries, logger *log.Logger) *Saver {
	return &Saver{queries: queries, logger: logger}
}

func (s *Saver) SaveTrainData(ctx context.Context, train *TrainData) error {
	params := db.UpsertTrainParams{
		TrainNo:          train.TrainNo,
		TrainName:        train.TrainName,
		TrainType:        train.TrainType,
		Zone:             sql.NullString{String: train.Zone, Valid: train.Zone != ""},
		ReturnTrainNo:    sql.NullInt64{Int64: train.ReturnTrainNo, Valid: train.ReturnTrainNo != 0},
		CoachComposition: sql.NullString{String: train.CoachComposition, Valid: train.CoachComposition != ""},
		SourceUrl:        train.SourceURL,
	}
	return s.queries.UpsertTrain(ctx, params)
}

func (s *Saver) SaveStationData(ctx context.Context, station *StationData) error {
	params := db.UpsertStationParams{
		StationCode:       station.StationCode,
		StationName:       station.StationName,
		Zone:              sql.NullString{String: station.Zone, Valid: station.Zone != ""},
		Division:          toNullString(station.Division),
		Address:           sql.NullString{String: station.Address, Valid: station.Address != ""},
		ElevationM:        toNullFloat64(station.ElevationM),
		Lat:               toNullFloat64(station.Lat),
		Lng:               toNullFloat64(station.Lng),
		NumberOfPlatforms: toNullInt64(station.NumberOfPlatforms),
		StationType:       toNullString(station.StationType),
		StationCategory:   toNullString(station.StationCategory),
		TrackType:         toNullString(station.TrackType),
	}
	return s.queries.UpsertStation(ctx, params)
}

func (s *Saver) SaveScheduleData(ctx context.Context, schedule *ScheduleData) error {
	params := db.UpsertTrainScheduleParams{
		TrainNo:               schedule.TrainNo,
		OriginStationCode:     schedule.OriginStationCode,
		TerminusStationCode:   schedule.TerminusStationCode,
		OriginSchDepartureMin: int64(schedule.OriginSchDepartureMin),
		TotalDistanceKm:       schedule.TotalDistanceKm,
		TotalRuntimeMin:       int64(schedule.TotalRuntimeMin),
		RunningDaysBitmap:     int64(schedule.RunningDaysBitmap),
	}
	scheduleID, err := s.queries.UpsertTrainSchedule(ctx, params)
	if err != nil {
		return err
	}
	for _, route := range schedule.Route {
		routeParams := db.UpsertTrainRouteParams{
			ScheduleID:               scheduleID,
			StationCode:              route.StationCode,
			DistanceKm:               route.DistanceKm,
			SchArrivalMinFromStart:   int64(route.SchArrivalMinFromStart),
			SchDepartureMinFromStart: int64(route.SchDepartureMinFromStart),
			Stops:                    int64(route.Stops),
		}
		if err := s.queries.UpsertTrainRoute(ctx, routeParams); err != nil {
			return err
		}
	}
	return nil
}

func toNullString(ptr *string) sql.NullString {
	if ptr == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *ptr, Valid: true}
}

func toNullFloat64(ptr *float64) sql.NullFloat64 {
	if ptr == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *ptr, Valid: true}
}

func toNullInt64(ptr *int) sql.NullInt64 {
	if ptr == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*ptr), Valid: true}
}
