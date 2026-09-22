package handler

import (
	"fmt"
	"net/http"
)

// SIMULATED: faux-seer has no anomaly detection model or historical timeseries
// data store. All four anomaly endpoints return empty/success acks so Sentry's
// alert-rule setup flow does not fail. The consumer
// (src/sentry/seer/anomaly_detection/get_anomaly_data.py) treats an empty
// timeseries as "no anomalies" and proceeds normally.

// anomalyDetect handles POST /v1/anomaly-detection/detect.
//
// SIMULATED: Real anomaly detection requires a trained timeseries model and
// historical data store, neither of which faux-seer provides. The consumer
// (get_anomaly_data_from_seer, get_anomaly_data.py:130-170) reads
// results.get("success"), results.get("timeseries"). An empty timeseries is
// valid — the consumer treats it as "no anomalies" and returns None.
func (s *Server) anomalyDetect(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode anomaly detect request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"timeseries": []any{},
	})
}

// anomalyAlertData handles POST /v1/anomaly-detection/alert-data.
//
// Consumer: get_anomaly_threshold_data_from_seer in
// src/sentry/seer/anomaly_detection/get_anomaly_data.py:280-320
// reads results.get("success"), results.get("data").
// Response type: SeerDetectorDataResponse = {success: bool, data: list}.
// An empty data list is valid — the consumer returns it as-is and callers
// treat empty threshold data as "no threshold info available".
func (s *Server) anomalyAlertData(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode anomaly alert-data request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    []any{},
	})
}

// anomalyStore handles POST /v1/anomaly-detection/store.
//
// Consumer: send_historical_data_to_seer_legacy in
// src/sentry/seer/anomaly_detection/store_data.py:270-300
// reads results.get("success") and results.get("message").
// Response type: StoreDataResponse = {success: bool, message?: str}.
// A success: true ack is sufficient — the consumer only raises on failure.
func (s *Server) anomalyStore(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode anomaly store request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// anomalyDeleteAlertData handles POST /v1/anomaly-detection/delete-alert-data.
//
// Consumer: delete_rule_in_seer in
// src/sentry/seer/anomaly_detection/delete_rule.py:85-100
// reads results.get("success") — explicitly checks it is not None and is True,
// otherwise treats the delete as failed.
// Response type: {success: bool, message?: str}.
// A success: true ack is required; missing or false would cause the consumer
// to log an error and return False.
func (s *Server) anomalyDeleteAlertData(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode anomaly delete-alert-data request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// workflowCompareCohort handles POST /v1/workflows/compare/cohort.
//
// Consumer: compare_distributions in
// src/sentry/seer/endpoints/compare.py:28-40, called from
// src/sentry/api/endpoints/organization_trace_item_attributes_ranked.py:109
// which reads scored_attrs_rrr.get("results", []) and iterates as (attr, score).
// Response type: {results: list[tuple[str, float]]}.
// An empty results list is valid — the consumer simply produces no ranked
// attributes, and the endpoint returns the baseline ranked_distribution as-is.
func (s *Server) workflowCompareCohort(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode compare cohort request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"results": []any{},
	})
}
