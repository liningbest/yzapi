// Command yzapi runs the YZ AI Gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"yzapi/internal/api"
	"yzapi/internal/bootstrap"
	"yzapi/internal/compliance"
	"yzapi/internal/config"
	"yzapi/internal/crypto"
	"yzapi/internal/db"
	"yzapi/internal/essink"
	"yzapi/internal/gateway"
	"yzapi/internal/logstore"
	"yzapi/internal/model"
	"yzapi/internal/routing"
	"yzapi/internal/server"
	"yzapi/internal/settings"
)

var version = "dev"

func main() {
	resetUser := flag.String("reset-password", "", "reset the password of the given user and exit")
	newPw := flag.String("password", "", "new password for -reset-password (random if empty)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("yzapi", version)
		return
	}

	cfg := config.Load()
	setupLogging(cfg)

	database, err := db.Open(cfg)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	if *resetUser != "" {
		if err := bootstrap.ResetAdminPassword(database, *resetUser, *newPw); err != nil {
			slog.Error("reset password", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := bootstrap.Run(cfg, database); err != nil {
		slog.Error("bootstrap", "err", err)
		os.Exit(1)
	}

	cipher, err := crypto.LoadOrCreateKey(cfg.DataDir)
	if err != nil {
		slog.Error("credential key", "err", err)
		os.Exit(1)
	}
	st, err := settings.New(database)
	if err != nil {
		slog.Error("settings", "err", err)
		os.Exit(1)
	}
	logs := logstore.New(database, func() int { return st.Get().Basic.LogRetentionDays })

	gw, err := gateway.New(cfg, database, cipher, st, logs)
	if err != nil {
		slog.Error("gateway", "err", err)
		os.Exit(1)
	}
	gw.AuditLogger = func(l *model.AuditLog) { go database.Create(l) }
	gw.DecisionLogger = func(d *model.RouteDecision) { go database.Create(d) }

	es := essink.New(st)
	mgmt, err := api.New(cfg, database, gw, st, cipher, api.Engines{ES: es}, version)
	if err != nil {
		slog.Error("api", "err", err)
		os.Exit(1)
	}
	embed := mgmt.VectorEmbedFunc()
	routeEng := routing.New(database, st, embed)
	compEng := compliance.New(database, st, embed)
	mgmt.SetEngines(api.Engines{Route: routeAdapter{routeEng}, Compliance: complianceAdapter{compEng}, ES: es})
	gw.SetRouter(routeAdapter{routeEng})
	gw.SetChecker(complianceAdapter{compEng})
	gw.BodySink = es
	st.OnApply(func(settings.All) {
		_ = routeEng.Reload()
		_ = compEng.Reload()
	})

	srv := server.New(cfg, database, gw, mgmt)
	go func() {
		slog.Info("yzapi listening", "addr", cfg.ListenAddr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down", "timeout", cfg.ShutdownTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = srv.Shutdown(ctx)
	logs.Close(ctx)
	es.Close(ctx)
	slog.Info("bye")
}

func setupLogging(cfg *config.Config) {
	lvl := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	var h slog.Handler
	if cfg.Dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	} else {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
			}
			return a
		}})
	}
	slog.SetDefault(slog.New(h))
}

// routeAdapter bridges routing.Result to the gateway/api result types.
type routeAdapter struct{ e *routing.Engine }

func (r routeAdapter) Decide(ctx context.Context, requestID, text string, msgCount int) gateway.RouteResult {
	res := r.e.Decide(ctx, requestID, text, msgCount)
	return gateway.RouteResult{Label: res.Label, Source: res.Source, Confidence: res.Confidence, GroupID: res.GroupID,
		TopK: res.TopK, Normalized: res.Normalized, LatencyMs: res.LatencyMs}
}

func (r routeAdapter) Reload() error { return r.e.Reload() }

// complianceAdapter bridges compliance.Verdict to gateway.ComplianceVerdict.
type complianceAdapter struct{ e *compliance.Engine }

func toVerdict(v compliance.Verdict) gateway.ComplianceVerdict {
	return gateway.ComplianceVerdict{Hit: v.Hit, Block: v.Block, Action: v.Action, RiskLevel: v.RiskLevel, DetectMethod: v.DetectMethod,
		PolicyGroup: v.PolicyGroup, PolicyID: v.PolicyID, Evidence: v.Evidence, Confidence: v.Confidence, Hits: v.Hits}
}

func (a complianceAdapter) Check(ctx context.Context, text string) gateway.ComplianceVerdict {
	return toVerdict(a.e.Check(ctx, text))
}

func (a complianceAdapter) Test(ctx context.Context, text string) gateway.ComplianceVerdict {
	return toVerdict(a.e.Test(ctx, text))
}

func (a complianceAdapter) Reload() error { return a.e.Reload() }

func (a complianceAdapter) BuildVectors(ctx context.Context, ids []uint) (int, int, error) {
	return a.e.BuildVectors(ctx, ids)
}

func (r routeAdapter) BuildVectors(ctx context.Context, ids []uint) (int, int, error) {
	return r.e.BuildVectors(ctx, ids)
}

func (r routeAdapter) Preview(ctx context.Context, text string, msgCount int) (api.PreviewResult, error) {
	res, err := r.e.Preview(ctx, text, msgCount)
	return api.PreviewResult{Label: res.Label, Source: res.Source, Confidence: res.Confidence, GroupID: res.GroupID,
		TopK: res.TopK, Normalized: res.Normalized, LatencyMs: res.LatencyMs}, err
}
