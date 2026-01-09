package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	v1 "trano/internal/api/schema/v1"
	db "trano/internal/db/sqlc"

	"google.golang.org/protobuf/proto"
)

type TrainHandler struct {
	queries *db.Queries
	db      *sql.DB
	logger  *log.Logger
}

func NewTrainHandler(queries *db.Queries, dbConn *sql.DB, logger *log.Logger) *TrainHandler {
	return &TrainHandler{
		queries: queries,
		db:      dbConn,
		logger:  logger,
	}
}

func (h *TrainHandler) GetLiveTrains(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	trains, err := h.queries.GetLiveTrains(ctx)
	if err != nil {
		h.logger.Printf("handler: live trains query failed: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := mapLiveTrains(trains)

	// Marshal to binary using protobuf
	data, err := proto.Marshal(resp)
	if err != nil {
		h.logger.Printf("handler: failed to marshal protobuf: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func mapLiveTrains(
	rows []db.GetLiveTrainsRow,
) *v1.LiveTrainsResponse {

	typeMap := map[string]uint32{}
	statusMap := map[string]uint32{}

	var types []*v1.TrainType
	var statuses []*v1.TrainStatus
	var trains []*v1.LiveTrain

	nextTypeID := uint32(1)
	nextStatusID := uint32(1)

	for _, r := range rows {
		// type
		typeID, ok := typeMap[r.TrainType]
		if !ok {
			typeID = nextTypeID
			nextTypeID++
			typeMap[r.TrainType] = typeID
			types = append(types, &v1.TrainType{
				Id:   typeID,
				Type: r.TrainType,
			})
		}

		// status
		status := "unknown"
		if s, ok := r.CurrentStatus.(string); ok {
			status = s
		}

		statusID, ok := statusMap[status]
		if !ok {
			statusID = nextStatusID
			nextStatusID++
			statusMap[status] = statusID
			statuses = append(statuses, &v1.TrainStatus{
				Id:     statusID,
				Status: status,
			})
		}

		// train
		train := &v1.LiveTrain{
			TrainNo:  uint32(r.TrainNo),
			Name:     r.TrainName,
			TypeId:   typeID,
			StatusId: statusID,
		}

		if r.LatU6.Valid {
			train.LatU6 = uint32(r.LatU6.Int64)
		}
		if r.LngU6.Valid {
			train.LngU6 = uint32(r.LngU6.Int64)
		}
		if r.BearingDeg.Valid {
			train.BearingDeg = uint32(r.BearingDeg.Int64)
		}

		trains = append(trains, train)
	}

	return &v1.LiveTrainsResponse{
		Types:     types,
		Statuses:  statuses,
		Trains:    trains,
		Total:     uint32(len(trains)),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}
