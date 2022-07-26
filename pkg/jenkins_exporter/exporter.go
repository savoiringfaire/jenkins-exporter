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
	numBuilds = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "build_count"),
		"Number of jenkins builds run",
		[]string{"job_name"},
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
	buildCount     map[string]int
	up             int
}

func NewExporter(jenkinsClient *gojenkins.Jenkins) Exporter {
	return Exporter{
		JenkinsClient:      jenkinsClient,
		JobRefreshInterval: 10 * time.Second,
		Ctx:                context.Background(),
		state: ExporterState{
			buildCount: make(map[string]int),
		},
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- numJobs
	ch <- up
	ch <- numBuilds
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(numJobs, prometheus.GaugeValue, float64(len(e.state.jobs)))
	ch <- prometheus.MustNewConstMetric(up, prometheus.GaugeValue, float64(e.state.up))

	for job, count := range e.state.buildCount {
		ch <- prometheus.MustNewConstMetric(numBuilds, prometheus.GaugeValue, float64(count), job)
	}
}

func (e *Exporter) refreshJobs() {
	log.Info("Updating job list")
	var err error

	e.state.jobs, err = e.JenkinsClient.GetAllJobs(e.Ctx)
	if err != nil {
		e.state.up = 0
		log.Errorf("Error refreshing jobs: %s", err)
		return
	}

	e.state.up = 1
	log.WithFields(log.Fields{
		"num_jobs": len(e.state.jobs),
	}).Info("Job list update finished")
}

func (e *Exporter) countBuildsForJob(job *gojenkins.Job) int {
	var builds int

	buildIds, err := job.GetAllBuildIds(e.Ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"job": job.Base,
		}).Errorf("Error getting builds for job: %s", err)
	}

	builds = len(buildIds)

	if len(job.GetInnerJobsMetadata()) > 0 {
		innerJobs, err := job.GetInnerJobs(e.Ctx)
		if err != nil {
			log.WithFields(log.Fields{
				"job": job.Base,
			}).Errorf("Error getting inner builds for job: %s", err)
		}

		for _, job := range innerJobs {
			builds = builds + e.countBuildsForJob(job)
		}
	}

	e.state.buildCount[job.Base] = builds
	return builds
}

func (e *Exporter) countBuilds() {
	for _, job := range e.state.jobs {
		e.countBuildsForJob(job)
	}
}

func (e *Exporter) Run() {
	for {
		if time.Now().Sub(e.state.lastJobRefresh) > e.JobRefreshInterval {
			e.refreshJobs()
			e.countBuilds()
		}

		time.Sleep(5 * time.Second)
	}
}
