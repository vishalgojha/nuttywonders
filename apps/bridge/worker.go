package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// runJobWorker drains the whatsapp_jobs table. Jobs are claimed in Postgres
// with skip locked, so several bridge replicas can share the queue safely.
func (s *Server) runJobWorker(ctx context.Context) {
	defer s.log.Info("job worker stopped")

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cfg.PollInterval):
		}

		sent, err := s.drainQueue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("job worker failed", "error", err)
			continue
		}
		if sent > 0 {
			s.log.Info("job worker sent messages", "count", sent)
		}
	}
}

func (s *Server) drainQueue(ctx context.Context) (int, error) {
	if cli := s.wa.cli(); cli == nil || !cli.IsConnected() {
		return 0, nil
	}

	jobs, err := s.claimJobs(ctx, 5)
	if err != nil {
		return 0, err
	}

	sent := 0
	for _, job := range jobs {
		sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.wa.SendText(sendCtx, job.ToPhone, job.Body)
		cancel()

		finishCtx, cancelFinish := context.WithTimeout(context.Background(), 15*time.Second)
		if finishErr := s.finishJob(finishCtx, job, err); finishErr != nil {
			s.log.Warn("could not update job", "job", job.ID, "error", finishErr)
		}
		cancelFinish()

		if err != nil {
			s.log.Warn("whatsapp job failed", "job", job.ID, "to", job.ToPhone, "error", err)
			// back off before the next claim so a broken socket is not hammered
			select {
			case <-ctx.Done():
				return sent, nil
			case <-time.After(s.cfg.SendGap):
			}
			continue
		}

		sent++
		select {
		case <-ctx.Done():
			return sent, nil
		case <-time.After(s.cfg.SendGap):
		}
	}

	if sent > 0 {
		s.refreshBroadcastCounts(ctx)
	}
	return sent, nil
}

func (s *Server) claimJobs(ctx context.Context, limit int) ([]WhatsAppJob, error) {
	var jobs []WhatsAppJob
	params := map[string]any{"p_worker": s.cfg.WorkerName, "p_limit": limit}
	if err := s.db.RPC(ctx, "claim_whatsapp_jobs", params, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *Server) finishJob(ctx context.Context, job WhatsAppJob, sendErr error) error {
	query := url.Values{}
	query.Set("id", "eq."+strconv.FormatInt(job.ID, 10))

	if sendErr == nil {
		return s.db.Update(ctx, "whatsapp_jobs", query, map[string]any{
			"status":     "sent",
			"sent_at":    time.Now().UTC().Format(time.RFC3339),
			"attempts":   job.Attempts + 1,
			"last_error": nil,
			"claimed_by": nil,
		}, nil)
	}

	attempts := job.Attempts + 1
	body := map[string]any{
		"attempts":   attempts,
		"last_error": truncate(sendErr.Error(), 400),
		"claimed_by": nil,
		"status":     "queued",
	}
	// Three tries is enough for a flaky socket; beyond that a human should look.
	if attempts >= 3 {
		body["status"] = "failed"
	}
	return s.db.Update(ctx, "whatsapp_jobs", query, body, nil)
}

// refreshBroadcastCounts keeps the admin broadcast list honest.
func (s *Server) refreshBroadcastCounts(ctx context.Context) {
	var sending []struct {
		ID         int64 `json:"id"`
		SentCount  int   `json:"sent_count"`
		TotalCount int   `json:"total_count"`
	}
	query := url.Values{}
	query.Set("select", "id")
	query.Set("status", "in.(queued,sending)")
	if err := s.db.Select(ctx, "broadcasts", query, &sending); err != nil {
		return
	}

	for _, broadcast := range sending {
		counts := map[string]int{}
		jobQuery := url.Values{}
		jobQuery.Set("broadcast_id", "eq."+strconv.FormatInt(broadcast.ID, 10))
		jobQuery.Set("select", "status")

		var jobs []WhatsAppJob
		if err := s.db.Select(ctx, "whatsapp_jobs", jobQuery, &jobs); err != nil {
			continue
		}
		for _, job := range jobs {
			counts[job.Status]++
		}

		update := url.Values{}
		update.Set("id", "eq."+strconv.FormatInt(broadcast.ID, 10))
		body := map[string]any{"sent_count": counts["sent"]}

		if counts["queued"] == 0 && counts["sending"] == 0 && counts["sent"] > 0 {
			body["status"] = "done"
			body["sent_at"] = time.Now().UTC().Format(time.RFC3339)
		} else if counts["queued"] > 0 {
			body["status"] = "sending"
		}
		_ = s.db.Update(ctx, "broadcasts", update, body, nil)
	}
}

// runRetentionSweep runs the housekeeping RPCs once an hour.
func (s *Server) runRetentionSweep(ctx context.Context) {
	defer s.log.Info("retention sweep stopped")

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if _, err := s.cancelExpiredJobs(ctx); err != nil {
			s.log.Warn("cleanup_expired_jobs failed", "error", err)
		}
		if _, err := s.releaseStaleJobs(ctx); err != nil {
			s.log.Warn("release_stale_jobs failed", "error", err)
		}
		if _, err := s.pruneMessages(ctx); err != nil {
			s.log.Warn("prune_whatsapp_messages failed", "error", err)
		}
	}
}

func orderConfirmationMessage(ctx context.Context, s *Server, order *Order) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s, your NuttyWonders order %s is confirmed.\n\n", firstName(order.CustomerName), order.Code)
	b.WriteString(orderSummary(order))
	b.WriteString("\n\nTotal to pay: " + formatINR(order.TotalINR) + "\n")

	if vpa := s.setting(ctx, "upi_vpa", ""); vpa != "" {
		fmt.Fprintf(&b, "UPI id: %s\n", vpa)
	}
	if instructions := s.setting(ctx, "payment_instructions", ""); instructions != "" {
		b.WriteString(instructions + "\n")
	}
	if base := s.setting(ctx, "app_base_url", ""); base != "" {
		b.WriteString("Track: " + base + "/order?code=" + order.Code + "\n")
	}
	if hours := s.setting(ctx, "business_hours", ""); hours != "" {
		b.WriteString(hours + "\n")
	}
	return strings.TrimSpace(b.String())
}

func orderSummary(order *Order) string {
	var b strings.Builder
	for _, item := range order.Items {
		fmt.Fprintf(&b, "%s x%d — %s\n", item.ProductName, item.Quantity, formatINR(item.LineTotalINR))
	}
	if order.ShippingINR > 0 {
		fmt.Fprintf(&b, "Delivery: %s\n", formatINR(order.ShippingINR))
	} else {
		b.WriteString("Delivery: free\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "there"
	}
	if idx := strings.IndexAny(trimmed, " ,"); idx > 0 {
		return trimmed[:idx]
	}
	return trimmed
}
