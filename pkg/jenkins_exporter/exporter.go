package jenkins_exporter

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"time"

	"github.com/bndr/gojenkins"
	log "github.com/sirupsen/logrus"
)

var (
	namespace = "jenkins"
	numJobs   = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "job_count"),
		"How many Jenkins jobs are currently defined on the server",
		[]string{},
		nil,
	)
	up = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"1 When everything is happy, 0 when we fail to scrape prometheus",
		[]string{},
		nil,
	)
)

type Exporter struct {
	Ctx context.Context

	JobRefreshInterval time.Duration

	JenkinsClient *gojenkins.Jenkins

	state ExporterState
}

type ExporterState struct {
	lastJobRefresh time.Time
	jobs           []*gojenkins.Job
	up             int
}

func NewExporter(jenkinsClient *gojenkins.Jenkins) Exporter {
	return Exporter{
		JenkinsClient:      jenkinsClient,
		JobRefreshInterval: 10 * time.Second,
		Ctx:                context.Background(),
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- numJobs
	ch <- up
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(numJobs, prometheus.GaugeValue, float64(len(e.state.jobs)))
	ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(e.state.up))
}

func (e *Exporter) refreshJobs() {
	log.Info("Updating job list")
	var err error

	e.state.jobs, err = e.JenkinsClient.GetAllJobs(e.Ctx)
	if err != nil {
		e.state.up = 0
		log.Errorf("Error refreshing jobs: %s", err)
	}

	e.state.up = 1
	log.WithFields(log.Fields{
		"num_jobs": len(e.state.jobs),
	}).Info("Job list update finished")
}

func (e *Exporter) Run() {
	for {
		if time.Now().Sub(e.state.lastJobRefresh) > e.JobRefreshInterval {
			e.refreshJobs()
		}

		time.Sleep(5 * time.Second)
	}
}
