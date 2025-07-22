package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

type Directory struct {
	Path  string `yaml:"path"`
	Label string `yaml:"label"`
}

type Config struct {
	Directories []Directory `yaml:"directories"`
}

type DirectorySizeExporter struct {
	directories []Directory
	desc        *prometheus.Desc
	timeout     time.Duration
	logger      *log.Logger
}

func NewDirectorySizeExporter(dirs []Directory, timeout time.Duration, logger *log.Logger) *DirectorySizeExporter {
	return &DirectorySizeExporter{
		directories: dirs,
		desc: prometheus.NewDesc(
			"directory_size_bytes",
			"Size of monitored directories in bytes",
			[]string{"name"},
			nil,
		),
		timeout: timeout,
		logger:  logger,
	}
}

// getDirSize calculates the size of a directory in bytes with timeout and context
func getDirSize(ctx context.Context, path string) (float64, error) {
	var total int64
	walkErrChan := make(chan error, 1)

	go func() {
		err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				total += info.Size()
			}
			// Check if context done during walking (early stop)
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
		})
		walkErrChan <- err
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case err := <-walkErrChan:
		if err != nil {
			return 0, err
		}
		return float64(total), nil
	}
}

func (e *DirectorySizeExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.desc
}

func (e *DirectorySizeExporter) Collect(ch chan<- prometheus.Metric) {
	for _, dir := range e.directories {
		e.logger.Printf("Collecting size for directory %q (path: %q)", dir.Label, dir.Path)
		ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
		size, err := getDirSize(ctx, dir.Path)
		cancel()
		if err != nil {
			e.logger.Printf("Error collecting directory %q size: %v", dir.Label, err)
			// Report 0 size but note failure with a metric or log (here just log)
			size = 0
		} else {
			e.logger.Printf("Directory %q size: %f bytes", dir.Label, size)
		}
		ch <- prometheus.MustNewConstMetric(e.desc, prometheus.GaugeValue, size, dir.Label)
	}
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	return &cfg, err
}

func main() {
	disableMetricsFlag := flag.Bool("disable-exporter-metrics", false, "Disable Prometheus default Go and process metrics")
	timeoutFlag := flag.Duration("collection-timeout", 15*time.Second, "Timeout for directory size collection")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./config.yml"
	}

	port := os.Getenv("EXPORTER_PORT")
	if port == "" {
		port = "9101"
	}

	logger.Printf("Loading config from %s", configPath)
	cfg, err := loadConfig(configPath)
	if err != nil {
		logger.Fatalf("Failed to load config: %v", err)
	}

	if *disableMetricsFlag {
		logger.Println("Disabling collection of exporter metrics (like go_*)")
		prometheus.Unregister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		prometheus.Unregister(collectors.NewGoCollector())
	} else {
		prometheus.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		prometheus.MustRegister(collectors.NewGoCollector())
	}

	exporter := NewDirectorySizeExporter(cfg.Directories, *timeoutFlag, logger)
	prometheus.MustRegister(exporter)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Graceful shutdown management
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Printf("Starting Directory Size Exporter on :%s — watching %d directories", port, len(cfg.Directories))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP server ListenAndServe error: %v", err)
		}
	}()

	// Wait for termination signal
	<-stop
	logger.Println("Shutdown signal received, shutting down HTTP server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Fatalf("HTTP server Shutdown error: %v", err)
	}

	wg.Wait()
	logger.Println("Exporter gracefully stopped")
}
