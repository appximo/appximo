package outbox

import "github.com/prometheus/client_golang/prometheus"

// Collector exposes an Observer's latest snapshot as Prometheus metrics. It is a
// read-only view over the atomic snapshot — a scrape never touches the database.
//
// The headline series is appximo_outbox_oldest_pending_age_seconds: depth alone
// lies (a thousand rows draining fast are healthy; one row stuck for a month is
// the incident), so alert on AGE, not depth.
type Collector struct {
	obs *Observer

	pending      *prometheus.Desc
	failed       *prometheus.Desc
	discarded    *prometheus.Desc
	sentLastHour *prometheus.Desc
	oldestPend   *prometheus.Desc
	oldestFail   *prometheus.Desc
	byTopic      *prometheus.Desc
	wfRuns       *prometheus.Desc
	wfFailed     *prometheus.Desc
	wfOverdue    *prometheus.Desc
	remFired     *prometheus.Desc
	remFailed    *prometheus.Desc
}

// NewCollector wraps obs for registration on a Prometheus registry.
func NewCollector(obs *Observer) *Collector {
	return &Collector{
		obs: obs,
		pending: prometheus.NewDesc("appximo_outbox_pending",
			"Outbox rows in state='pending' (enqueued, not yet delivered)", nil, nil),
		failed: prometheus.NewDesc("appximo_outbox_failed",
			"Outbox rows parked in state='failed' (retries exhausted; each carries last_error)", nil, nil),
		discarded: prometheus.NewDesc("appximo_outbox_discarded",
			"Outbox rows parked in state='discarded' by a processor's decision (reason in last_error; never delivered, never retried)", nil, nil),
		sentLastHour: prometheus.NewDesc("appximo_outbox_sent_last_hour",
			"Outbox rows marked sent in the last hour (drain-rate proxy)", nil, nil),
		oldestPend: prometheus.NewDesc("appximo_outbox_oldest_pending_age_seconds",
			"Age of the OLDEST pending outbox row — the health metric that matters (0 when the queue is empty)", nil, nil),
		oldestFail: prometheus.NewDesc("appximo_outbox_oldest_failed_age_seconds",
			"Age of the oldest failed outbox row (0 when none)", nil, nil),
		byTopic: prometheus.NewDesc("appximo_outbox_pending_by_topic",
			"Pending outbox rows per topic (bounded: the 20 oldest-backlog topics)", []string{"topic"}, nil),
		wfRuns: prometheus.NewDesc("appximo_workflow_runs_24h",
			"Workflow runs started in the last 24 hours", nil, nil),
		wfFailed: prometheus.NewDesc("appximo_workflow_failed_24h",
			"Workflow runs that FAILED in the last 24 hours (error + per-step detail in public.workflow_runs)", nil, nil),
		wfOverdue: prometheus.NewDesc("appximo_workflow_overdue_seconds",
			"How far past due the most-overdue cron workflow schedule is (growing = no scheduler is firing)", nil, nil),
		remFired: prometheus.NewDesc("appximo_workflow_reminders_fired_24h",
			"Per-row reminders (time triggers) claimed and fired in the last 24 hours — one per (workflow, row, due instant)", nil, nil),
		remFailed: prometheus.NewDesc("appximo_workflow_reminders_failed_24h",
			"Reminder runs that FAILED in the last 24 hours (claim released; retried on the next sweep while inside grace)", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.pending
	ch <- c.failed
	ch <- c.discarded
	ch <- c.sentLastHour
	ch <- c.oldestPend
	ch <- c.oldestFail
	ch <- c.remFired
	ch <- c.remFailed
	ch <- c.byTopic
	ch <- c.wfRuns
	ch <- c.wfFailed
	ch <- c.wfOverdue
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	s := c.obs.Latest()
	if s == nil {
		return // before the first collection: no series beats a lying zero
	}
	ch <- prometheus.MustNewConstMetric(c.pending, prometheus.GaugeValue, float64(s.Pending))
	ch <- prometheus.MustNewConstMetric(c.failed, prometheus.GaugeValue, float64(s.Failed))
	ch <- prometheus.MustNewConstMetric(c.discarded, prometheus.GaugeValue, float64(s.Discarded))
	ch <- prometheus.MustNewConstMetric(c.sentLastHour, prometheus.GaugeValue, float64(s.SentLastHour))
	ch <- prometheus.MustNewConstMetric(c.oldestPend, prometheus.GaugeValue, s.OldestPendingAge)
	ch <- prometheus.MustNewConstMetric(c.oldestFail, prometheus.GaugeValue, s.OldestFailedAge)
	for _, tc := range s.PendingByTopic {
		ch <- prometheus.MustNewConstMetric(c.byTopic, prometheus.GaugeValue, float64(tc.Count), tc.Topic)
	}
	ch <- prometheus.MustNewConstMetric(c.wfRuns, prometheus.GaugeValue, float64(s.WorkflowRuns24h))
	ch <- prometheus.MustNewConstMetric(c.wfFailed, prometheus.GaugeValue, float64(s.WorkflowFailed24h))
	ch <- prometheus.MustNewConstMetric(c.wfOverdue, prometheus.GaugeValue, s.WorkflowOverdueSeconds)
	ch <- prometheus.MustNewConstMetric(c.remFired, prometheus.GaugeValue, float64(s.RemindersFired24h))
	ch <- prometheus.MustNewConstMetric(c.remFailed, prometheus.GaugeValue, float64(s.RemindersFailed24h))
}
