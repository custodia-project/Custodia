// Package coverage implements the Python coverage v1 classification contract.
package coverage

import "time"

type Evidence struct {
	Identity  int `json:"identity_records"`
	Users     int `json:"netskope_users"`
	Recent    int `json:"devices_recent"`
	Old       int `json:"devices_not_recent"`
	Ambiguous int `json:"devices_ambiguous"`
}

// Fresh rejects future timestamps as well as missing, partial and expired runs.
func Fresh(status string, completed time.Time, now time.Time) bool {
	age := now.Sub(completed)
	return status == "completed" && !completed.IsZero() && age >= 0 && age <= 24*time.Hour
}

func Classify(e Evidence, fresh bool) string {
	if !fresh || e.Identity != 1 || e.Users != 1 || e.Ambiguous != 0 {
		return "unknown"
	}
	if e.Recent > 0 {
		if e.Old > 0 {
			return "partial"
		}
		return "covered"
	}
	return "gap"
}
