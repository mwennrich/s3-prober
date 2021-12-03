package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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
	flagSkipmakedeletebucket = "skipmakedeletebucket"
	envSkipmakedeletebucket  = "SKIPMAKEDELETEBUCKET"
	flagFilename         = "filename"
	envFilename          = "FILENAME"

	s3Success = prometheus.NewDesc(
		"probe_success",
		"Displays whether or not the probe was a success",
		[]string{"operation", "s3_endpoint"}, nil,
	)
	s3Duration = prometheus.NewDesc(
		"probe_duration_seconds",
		"Returns how long the probe took to complete in seconds",
		[]string{"operation", "s3_endpoint"}, nil,
	)
)

// Exporter is our exporter type
type Exporter struct {
	bucket           string
	endpoint         string
	accessKey        string
	secretKey        string
	filename         string
	skipmakedeletebucket bool
	mutex            *sync.Mutex
}

// Describe all the metrics we export
func (e Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- s3Success
	ch <- s3Duration
}

// Collect metrics
func (e Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	minioClient, err := minio.New(e.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(e.accessKey, e.secretKey, ""),
		Secure: true,
		Region: "us-east-1",
	})
	if err != nil {
		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "connect", e.endpoint,
		)
		klog.Errorf("Could not create minioClient to endpoint %s, %v\n", e.endpoint, err)
		return
	}

	_, object := filepath.Split(e.filename)

	bs, err := minioClient.ListBuckets(context.Background())
	if err != nil {
		return
	}
	found := false
	for _, b := range bs {
		if b.Name == e.bucket {
			found = true
		}
	}
	if !e.skipmakedeletebucket || !found {
		err = measure(e, "makebucket", ch, func() error { return minioClient.MakeBucket(context.Background(), e.bucket, minio.MakeBucketOptions{}) })
		if err != nil {
			// return if makebucket failed
			if err != nil {
				klog.Errorf("error during MakeBucket: %w", err)
			}
			return
		}
	}
	err = measure(e, "put", ch, func() error {
		_, err := minioClient.FPutObject(context.Background(), e.bucket, object, e.filename, minio.PutObjectOptions{})
		if err != nil {
			klog.Errorf("error during PutObject: %w", err)
		}
		return err
	})
	// only if put succeeded
	if err == nil {
		err = measure(e, "get", ch, func() error {
			return minioClient.FGetObject(context.Background(), e.bucket, object, "/tmp/"+object, minio.GetObjectOptions{})
		})
		if err != nil {
			klog.Errorf("error during GetObject: %w", err)
		}
		err = measure(e, "stat", ch, func() error {
			_, err := minioClient.StatObject(context.Background(), e.bucket, object, minio.StatObjectOptions{})
			return err
		})
		if err != nil {
			klog.Errorf("error during StatObject: %w", err)
		}
		err = measure(e, "remove", ch, func() error {
			return minioClient.RemoveObject(context.Background(), e.bucket, object, minio.RemoveObjectOptions{})
		})
		if err != nil {
			klog.Errorf("error during RemoveObject: %w", err)
		}
	}
	if !e.skipmakedeletebucket {
		err = measure(e, "removebucket", ch, func() error { return minioClient.RemoveBucket(context.Background(), e.bucket) })
		if err != nil {
			klog.Errorf("error during RemoveBucket: %w", err)
		}
	}

}

func measure(e Exporter, operation string, ch chan<- prometheus.Metric, f func() error) error {
	// job = remove
	success := 1.0
	start := time.Now()
	err := f()
	if err != nil {
		success = 0
	}
	elapsed := time.Since(start)
	ch <- prometheus.MustNewConstMetric(
		s3Success, prometheus.GaugeValue, success, operation, e.endpoint,
	)
	ch <- prometheus.MustNewConstMetric(
		s3Duration, prometheus.GaugeValue, elapsed.Seconds(), operation, e.endpoint,
	)
	return err
}

func probeHandler(w http.ResponseWriter, r *http.Request, e Exporter) {
	registry := prometheus.NewRegistry()
	err := registry.Register(e)
	if err != nil {
		klog.Errorf("failed ot register prometheus: %w", err)
	}

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
			&cli.BoolFlag{
				Name:    flagSkipmakedeletebucket,
				Usage:   "Optional. Measure skipmakedeletebucket operations",
				EnvVars: []string{envSkipmakedeletebucket},
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
		bucket:           bucket,
		accessKey:        accessKey,
		secretKey:        secretKey,
		endpoint:         endpoint,
		filename:         filename,
		skipmakedeletebucket: c.Bool(flagSkipmakedeletebucket),
		mutex:            &sync.Mutex{},
	}

	klog.Infoln("Starting s3_prober", version.Info())
	klog.Infoln("Build context", version.BuildContext())

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, exporter)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(`<html>
						 <head><title>S3 Prober</title></head>
						 <body>
						 <h1>S3 Prober</h1>
						 <p><a href='/metrics'>Metrics</a></p>
						 </body>
						 </html>`))
		if err != nil {
			klog.Error(err)
		}

	})

	klog.Infoln("Listening on", listenAddress)
	klog.Fatal(http.ListenAndServe(listenAddress, nil))
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
