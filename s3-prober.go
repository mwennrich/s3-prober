package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"github.com/urfave/cli/v2"
	"k8s.io/klog"

	"github.com/minio/minio-go/v6"
)

var (
	flagListenAddress    = "listen"
	envListenAddress     = "LISTEN_ADDRESS"
	defaultListenAddress = ":2112"
	flagAccessKey        = "accesskey"
	envAccessKey         = "ACCESSKEY"
	flagSecretKey        = "secretkey"
	envSecretKey         = "SECRETKEY"
	flagEndpoint         = "endpoint"
	envEndpoint          = "ENDPOINT"
	flagBucket           = "bucket"
	envBucket            = "BUCKET"
	flagLocation         = "location"
	envLocation          = "LOCATION"
	flagFilename         = "filename"
	envFilename          = "FILENAME"

	s3Success = prometheus.NewDesc(
		"probe_success",
		"Displays whether or not the probe was a success",
		[]string{"operation", "s3endpoint"}, nil,
	)
	s3Duration = prometheus.NewDesc(
		"probe_duration_seconds",
		"Returns how long the probe took to complete in seconds",
		[]string{"operation", "s3-endpoint"}, nil,
	)
)

// Exporter is our exporter type
type Exporter struct {
	bucket    string
	endpoint  string
	accessKey string
	secretKey string
	location  string
	filename  string
}

// Describe all the metrics we export
func (e Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- s3Success
	ch <- s3Duration
}

// Collect metrics
func (e Exporter) Collect(ch chan<- prometheus.Metric) {

	minioClient, err := minio.New(e.endpoint, e.accessKey, e.secretKey, true)
	if err != nil {
		klog.Fatalf("Could not create minioClient to endpoint %s, %v\n", e.endpoint, err)
		return
	}

	_, object := filepath.Split(e.filename)

	measure(e, "makebucket", ch, func() error { return minioClient.MakeBucket(e.bucket, e.location) })
	measure(e, "put", ch, func() error {
		_, err := minioClient.FPutObject(e.bucket, object, e.filename, minio.PutObjectOptions{})
		return err
	})
	measure(e, "get", ch, func() error { return minioClient.FGetObject(e.bucket, object, "/tmp"+object, minio.GetObjectOptions{}) })
	measure(e, "stat", ch, func() error {
		_, err := minioClient.StatObject(e.bucket, object, minio.StatObjectOptions{})
		return err
	})
	measure(e, "remove", ch, func() error { return minioClient.RemoveObject(e.bucket, object) })
	measure(e, "removebucket", ch, func() error { return minioClient.RemoveBucket(e.bucket) })

}

func measure(e Exporter, operation string, ch chan<- prometheus.Metric, f func() error) {
	// job = remove
	success := 1.0
	start := time.Now()
	err := f()
	if err != nil {
		success = 0
		klog.Error(err)
	}
	elapsed := time.Since(start)
	ch <- prometheus.MustNewConstMetric(
		s3Success, prometheus.GaugeValue, success, operation, e.endpoint,
	)
	ch <- prometheus.MustNewConstMetric(
		s3Duration, prometheus.GaugeValue, elapsed.Seconds(), operation, e.endpoint,
	)
}

func probeHandler(w http.ResponseWriter, r *http.Request, e Exporter) {

	registry := prometheus.NewRegistry()
	registry.Register(e)

	// Serve
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

func init() {
	prometheus.MustRegister(version.NewCollector("s3_prober"))
}

func startCmd() *cli.Command {
	return &cli.Command{
		Name: "start",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    flagListenAddress,
				Usage:   "Optional. Specify listen address.",
				EnvVars: []string{envListenAddress},
				Value:   defaultListenAddress,
			},
			&cli.StringFlag{
				Name:    flagAccessKey,
				Usage:   "Required. Specify s3 access key.",
				EnvVars: []string{envAccessKey},
			},
			&cli.StringFlag{
				Name:    flagSecretKey,
				Usage:   "Required. Specify s3 secret key.",
				EnvVars: []string{envSecretKey},
			},
			&cli.StringFlag{
				Name:    flagEndpoint,
				Usage:   "Required. Specify s3 endpoint url.",
				EnvVars: []string{envEndpoint},
			},
			&cli.StringFlag{
				Name:    flagBucket,
				Usage:   "Required. Specify s3 bucket name.",
				EnvVars: []string{envBucket},
			},
			&cli.StringFlag{
				Name:    flagFilename,
				Usage:   "Required. Specify filename.",
				EnvVars: []string{envFilename},
			},
			&cli.StringFlag{
				Name:    flagLocation,
				Usage:   "Optional. Specify s3 location.",
				EnvVars: []string{envLocation},
			},
		},
		Action: func(c *cli.Context) error {
			if err := startDaemon(c); err != nil {
				klog.Fatalf("Error starting daemon: %v", err)
				return err
			}
			return nil
		},
	}
}

func startDaemon(c *cli.Context) error {

	listenAddress := c.String(flagListenAddress)
	location := c.String(flagLocation)
	bucket := c.String(flagBucket)
	if bucket == "" {
		return fmt.Errorf("invalid empty flag %v", flagBucket)
	}
	endpoint := c.String(flagEndpoint)
	if endpoint == "" {
		return fmt.Errorf("invalid empty flag %v", flagEndpoint)
	}
	accessKey := c.String(flagAccessKey)
	if accessKey == "" {
		return fmt.Errorf("invalid empty flag %v", flagAccessKey)
	}
	secretKey := c.String(flagSecretKey)
	if secretKey == "" {
		return fmt.Errorf("invalid empty flag %v", flagSecretKey)
	}
	filename := c.String(flagFilename)
	if filename == "" {
		return fmt.Errorf("invalid empty flag %v", flagFilename)
	}

	exporter := Exporter{
		bucket:    bucket,
		accessKey: accessKey,
		secretKey: secretKey,
		endpoint:  endpoint,
		location:  location,
		filename:  filename,
	}

	log.Infoln("Starting s3_prober", version.Info())
	log.Infoln("Build context", version.BuildContext())

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, exporter)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
						 <head><title>S3 Prober</title></head>
						 <body>
						 <h1>S3 Prober</h1>
						 <p><a href='/metrics'>Metrics</a></p>
						 </body>
						 </html>`))
	})

	log.Infoln("Listening on", listenAddress)
	log.Fatal(http.ListenAndServe(listenAddress, nil))
	return nil
}

func main() {
	a := cli.NewApp()
	a.Usage = "S3 Prober"
	a.Commands = []*cli.Command{
		startCmd(),
	}

	if err := a.Run(os.Args); err != nil {
		klog.Fatalf("Critical error: %v", err)
	}
}
