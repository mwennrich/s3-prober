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
	"github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/cli/v2"
	"k8s.io/klog/v2"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	flagListenAddress        = "listen"
	envListenAddress         = "LISTEN_ADDRESS"
	defaultListenAddress     = ":2112"
	flagOpTimeout            = "timeout"
	envOpTimeout             = "OP_TIMEOUT"
	defaultOpTimeout         = 10
	flagAccessKey            = "accesskey"
	envAccessKey             = "ACCESSKEY"
	flagSecretKey            = "secretkey"
	envSecretKey             = "SECRETKEY"
	flagEndpoint             = "endpoint"
	envEndpoint              = "ENDPOINT"
	flagBucket               = "bucket"
	envBucket                = "BUCKET"
	flagSkipmakedeletebucket = "skipmakedeletebucket"
	envSkipmakedeletebucket  = "SKIPMAKEDELETEBUCKET"
	flagFilename             = "filename"
	envFilename              = "FILENAME"

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
	bucket               string
	endpoint             string
	accessKey            string
	secretKey            string
	filename             string
	skipmakedeletebucket bool
	mutex                *sync.Mutex
	opTimeout            int
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
		Transport: &http.Transport{
			ResponseHeaderTimeout: time.Second * time.Duration(e.opTimeout),
		},
	})
	if err != nil {
		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "connect", e.endpoint,
		)
		klog.Errorf("Could not create minioClient to endpoint %s, %v\n", e.endpoint, err)
		return
	}

	_, object := filepath.Split(e.filename)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(e.opTimeout))
	defer cancel()

	// list buckets
	success := 1.0
	start := time.Now()
	bs, err := minioClient.ListBuckets(ctx)
	if err != nil {
		success = 0
	}
	elapsed := time.Since(start)
	ch <- prometheus.MustNewConstMetric(
		s3Success, prometheus.GaugeValue, success, "listbuckets", e.endpoint,
	)
	ch <- prometheus.MustNewConstMetric(
		s3Duration, prometheus.GaugeValue, elapsed.Seconds(), "listbuckets", e.endpoint,
	)

	if err != nil {
		klog.Errorf("Failed to list buckets on endpoint %s, %v\n", e.endpoint, err)
		if !e.skipmakedeletebucket {
			ch <- prometheus.MustNewConstMetric(
				s3Success, prometheus.GaugeValue, 0, "makebucket", e.endpoint,
			)
			ch <- prometheus.MustNewConstMetric(
				s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "makebucket", e.endpoint,
			)
			ch <- prometheus.MustNewConstMetric(
				s3Success, prometheus.GaugeValue, 0, "removebucket", e.endpoint,
			)
			ch <- prometheus.MustNewConstMetric(
				s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "makebucket", e.endpoint,
			)
		}
		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "put", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "put", e.endpoint,
		)

		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "get", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "get", e.endpoint,
		)

		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "stat", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "stat", e.endpoint,
		)

		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "remove", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "remove", e.endpoint,
		)

		return
	}
	found := false
	for _, b := range bs {
		if b.Name == e.bucket {
			found = true
		}
	}
	if !e.skipmakedeletebucket || !found {
		err = measure(e, "makebucket", ch, func() error { return minioClient.MakeBucket(ctx, e.bucket, minio.MakeBucketOptions{}) })
		if err != nil {
			klog.Errorf("error during MakeBucket: %v", err)
			return
		}
	}
	err = measure(e, "put", ch, func() error {
		_, err := minioClient.FPutObject(ctx, e.bucket, object, e.filename, minio.PutObjectOptions{})
		if err != nil {
			klog.Errorf("error during PutObject: %v", err)
		}
		return err
	})
	// only if put succeeded
	if err == nil {
		err = measure(e, "get", ch, func() error {
			return minioClient.FGetObject(ctx, e.bucket, object, "/tmp/"+object, minio.GetObjectOptions{})
		})
		if err != nil {
			klog.Errorf("error during GetObject: %v", err)
		}
		err = measure(e, "stat", ch, func() error {
			_, err := minioClient.StatObject(ctx, e.bucket, object, minio.StatObjectOptions{})
			return err
		})
		if err != nil {
			klog.Errorf("error during StatObject: %v", err)
		}
		err = measure(e, "remove", ch, func() error {
			return minioClient.RemoveObject(ctx, e.bucket, object, minio.RemoveObjectOptions{})
		})
		if err != nil {
			klog.Errorf("error during RemoveObject: %v", err)
		}
	} else {
		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "get", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "get", e.endpoint,
		)

		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "stat", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "stat", e.endpoint,
		)

		ch <- prometheus.MustNewConstMetric(
			s3Success, prometheus.GaugeValue, 0, "remove", e.endpoint,
		)
		ch <- prometheus.MustNewConstMetric(
			s3Duration, prometheus.GaugeValue, float64(e.opTimeout), "remove", e.endpoint,
		)
	}
	if !e.skipmakedeletebucket {
		err = measure(e, "removebucket", ch, func() error { return minioClient.RemoveBucket(ctx, e.bucket) })
		if err != nil {
			klog.Errorf("error during RemoveBucket: %v", err)
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
		klog.Errorf("failed ot register prometheus: %v", err)
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
			&cli.IntFlag{
				Name:    flagOpTimeout,
				Usage:   "Optional. Timeout in seconds after which an operation is considered as failed.",
				EnvVars: []string{envOpTimeout},
				Value:   defaultOpTimeout,
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
	opTimeout := c.Int(flagOpTimeout)
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
		bucket:               bucket,
		accessKey:            accessKey,
		secretKey:            secretKey,
		endpoint:             endpoint,
		filename:             filename,
		skipmakedeletebucket: c.Bool(flagSkipmakedeletebucket),
		mutex:                &sync.Mutex{},
		opTimeout:            opTimeout,
	}

	klog.Infof("Starting s3_prober (op timeout %ds, skipmakedeletebucket %v)\n", opTimeout, c.Bool(flagSkipmakedeletebucket))
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
	server := &http.Server{
		Addr:              listenAddress,
		ReadHeaderTimeout: 1 * time.Minute,
	}

	klog.Fatal(server.ListenAndServe())
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
