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
		[]string{"job_name", "build_status"},
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
	lastJobRefresh  time.Time
	jobs            []*gojenkins.Job
	lastBuildID     map[string]int64
	buildCounter    map[string]map[string]int64
	inProgressQueue map[string][]*gojenkins.Build
	up              int
}

func NewExporter(jenkinsClient *gojenkins.Jenkins) Exporter {
	return Exporter{
		JenkinsClient:      jenkinsClient,
		JobRefreshInterval: 10 * time.Second,
		Ctx:                context.Background(),
		state: ExporterState{
			lastBuildID:     make(map[string]int64),
			buildCounter:    make(map[string]map[string]int64),
			inProgressQueue: make(map[string][]*gojenkins.Build),
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

	for job, statusCount := range e.state.buildCounter {
		for status, count := range statusCount {
			ch <- prometheus.MustNewConstMetric(numBuilds, prometheus.CounterValue, float64(count), job, status)
		}
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

func (e *Exporter) countBuildsForJob(job *gojenkins.Job) map[string]int64 {
	builds := make(map[string]int64)

	if len(e.state.inProgressQueue[job.Base]) > 0 {
		log.WithFields(log.Fields{
			"job": job.Base,
		}).Infof("Re-processing previously in-progress builds")

		for i, build := range e.state.inProgressQueue[job.Base] {
			build, err := job.GetBuild(e.Ctx, build.GetBuildNumber())
			if err != nil {
				log.WithFields(log.Fields{
					"job":   job.Base,
					"build": build.GetBuildNumber(),
				}).Errorf("Error getting details for build: %s", err)
				continue
			}

			if build.Raw.Building {
				log.WithFields(log.Fields{
					"job":   job.Base,
					"build": build.GetBuildNumber(),
				}).Infof("Job was still building, skipping.")

				continue
			}

			log.WithFields(log.Fields{
				"job":    job.Base,
				"build":  build.GetBuildNumber(),
				"status": build.Raw.Result,
			}).Infof("Previously building job is now finished.")

			builds[build.Raw.Result] = builds[build.Raw.Result] + 1

			e.state.inProgressQueue[job.Base] = append(e.state.inProgressQueue[job.Base][:i], e.state.inProgressQueue[job.Base][i+1:]...)
		}
	}

	buildIds, err := job.GetAllBuildIds(e.Ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"job": job.Base,
		}).Errorf("Error getting builds for job: %s", err)
	}

	for _, id := range buildIds {
		if _, ok := e.state.lastBuildID[job.Base]; !ok {
			e.state.lastBuildID[job.Base] = id.Number
			break
		}

		if e.state.lastBuildID[job.Base] == id.Number {
			break
		}

		buildDetails, err := job.GetBuild(e.Ctx, id.Number)
		if err != nil {
			log.WithFields(log.Fields{
				"job":   job.Base,
				"build": id.Number,
			}).Errorf("Error getting details for build: %s", err)
			continue
		}

		if buildDetails.Raw.Building {
			log.WithFields(log.Fields{
				"job":   job.Base,
				"build": id.Number,
			}).Infof("Job was still building, adding to queue.")

			e.state.inProgressQueue[job.Base] = append(e.state.inProgressQueue[job.Base], buildDetails)

			continue
		}

		log.WithFields(log.Fields{
			"job":    job.Base,
			"build":  id.Number,
			"status": buildDetails.Raw.Result,
		}).Infof("Retrieved details for build")

		builds[buildDetails.Raw.Result] = builds[buildDetails.Raw.Result] + 1
	}

	if len(buildIds) > 0 {
		e.state.lastBuildID[job.Base] = buildIds[0].Number
	}

	if len(job.GetInnerJobsMetadata()) > 0 {
		innerJobs, err := job.GetInnerJobs(e.Ctx)
		if err != nil {
			log.WithFields(log.Fields{
				"job": job.Base,
			}).Errorf("Error getting inner builds for job: %s", err)
		}

		for _, job := range innerJobs {
			for status, count := range e.countBuildsForJob(job) {
				builds[status] = builds[status] + count
			}
		}
	}

	for status, count := range builds {
		if _, ok := e.state.buildCounter[job.Base]; !ok {
			e.state.buildCounter[job.Base] = make(map[string]int64)
		}

		e.state.buildCounter[job.Base][status] = e.state.buildCounter[job.Base][status] + count
	}

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
