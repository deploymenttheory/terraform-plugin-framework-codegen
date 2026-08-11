package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
	auditrun "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/run"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

func newAuditCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "audit",
		Short: "exercise the live API to learn its true behaviour",
	}
	group.AddCommand(newAuditRunCommand())
	group.AddCommand(newAuditCleanupCommand())
	return group
}

// auditRunsDir is where the audit activity ledgers live, beside the
// observations. Never committed — it records live objects in somebody's
// tenant — but stable across runs, so a crashed run's ledger is exactly
// where the next cleanup looks.
const auditRunsDir = "audit/runs"

// newAuditRunCommand executes the derived plan against the live API: the
// only verb that touches a network with credentials. Observations land in
// --out, one file per entity, and a summary table says how far the run
// got.
func newAuditRunCommand() *cobra.Command {
	var (
		dir           string
		cfgFile       string
		out           string
		baseURL       string
		forceAPIAudit bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "execute the audit plan against the live API and record observations",
		Long: "Execute the audit plan against the live API and record observations.\n\n" +
			"Warning: the audit creates and deletes real objects in the tenant the\n" +
			"base URL points at. Run it only against sandbox or non-production\n" +
			"tenants; the toolkit does not police this — it is the operator's\n" +
			"responsibility.",
		Args: exactArgs("tfpfgen audit run [--dir spec] [--config tfpfgen.yaml] [--out audit/observations] [--base-url URL] [--force-api-audit]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, doc, lock, err := auditPlan(dir, cfgFile)
			if err != nil {
				return err
			}
			base, err := auditBaseURL(baseURL, cfg, dir)
			if err != nil {
				return err
			}
			if missing := config.MissingSecrets(cfg.Auth.Method, os.LookupEnv); len(missing) > 0 {
				return fmt.Errorf("auth.method %s needs secrets that are not set: %v", cfg.Auth.Method, missing)
			}

			obs, sum, runErr := auditrun.Run(cmd.Context(), auditrun.Options{
				Plan:    p,
				Doc:     doc,
				Config:  cfg,
				BaseURL: base,
				Auth: auditrun.Auth{
					Method:       cfg.Auth.Method,
					APIKeyHeader: cfg.Auth.APIKeyHeader,
					TokenURL:     cfg.Auth.TokenURL,
				},
				NamePrefix:    cfg.Audit.NamePrefix,
				RateLimitRPS:  cfg.Audit.RateLimitRPS,
				RunsDir:       auditRunsDir,
				SpecHash:      lock.SHA256,
				ForceAPIAudit: forceAPIAudit,
				Logger:        auditLogger(cmd.ErrOrStderr()),
			})

			// Whatever was learned is written, even when the run ended
			// early: partial evidence beats none, and the summary says how
			// partial.
			if len(obs) > 0 {
				if writeErr := observe.Write(out, obs); writeErr != nil {
					if runErr != nil {
						return fmt.Errorf("%v; additionally %w", runErr, writeErr)
					}
					return writeErr
				}
			}
			printSummary(cmd.OutOrStdout(), out, len(obs), sum)
			return runErr
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the revised document and the upstream pin")
	cmd.Flags().StringVar(&cfgFile, "config", "tfpfgen.yaml", "config file naming the auth method and audit bounds")
	cmd.Flags().StringVar(&out, "out", "audit/observations", "directory the observation files are written into")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "override the audited API's base URL")
	cmd.Flags().BoolVar(&forceAPIAudit, "force-api-audit", false, "mutate even when the tenant already holds more foreign objects than audit.max_objects")
	return cmd
}

// newAuditCleanupCommand clears the tenant of audit debris: ledgered
// objects by id, then everything carrying the name prefix. Runs
// automatically at both boundaries of every audit; this verb is the
// standalone invocation for after a crash.
func newAuditCleanupCommand() *cobra.Command {
	var (
		dir     string
		cfgFile string
		baseURL string
		prefix  string
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "delete the live test objects a previous audit left behind",
		Args:  exactArgs("tfpfgen audit cleanup [--dir spec] [--config tfpfgen.yaml] [--base-url URL] [--prefix NAME]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, p, _, _, err := auditPlan(dir, cfgFile)
			if err != nil {
				return err
			}
			base, err := auditBaseURL(baseURL, cfg, dir)
			if err != nil {
				return err
			}
			if prefix == "" {
				prefix = cfg.Audit.NamePrefix
			}
			if missing := config.MissingSecrets(cfg.Auth.Method, os.LookupEnv); len(missing) > 0 {
				return fmt.Errorf("auth.method %s needs secrets that are not set: %v", cfg.Auth.Method, missing)
			}

			sum, err := auditrun.Cleanup(cmd.Context(), auditrun.Options{
				Plan:    p,
				BaseURL: base,
				Auth: auditrun.Auth{
					Method:       cfg.Auth.Method,
					APIKeyHeader: cfg.Auth.APIKeyHeader,
					TokenURL:     cfg.Auth.TokenURL,
				},
				NamePrefix:   prefix,
				RateLimitRPS: cfg.Audit.RateLimitRPS,
				RunsDir:      auditRunsDir,
				Logger:       auditLogger(cmd.ErrOrStderr()),
			})

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "cleanup: %d deleted from ledgers, %d deleted by prefix %q\n",
				sum.LedgerDeletes, sum.PrefixDeletes, prefix)
			for _, o := range sum.Orphans {
				fmt.Fprintf(w, "  orphan: %s\n", o)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "spec", "directory holding the revised document")
	cmd.Flags().StringVar(&cfgFile, "config", "tfpfgen.yaml", "config file naming the auth method and name prefix")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "override the audited API's base URL")
	cmd.Flags().StringVar(&prefix, "prefix", "", "override the name prefix to match by (default audit.name_prefix)")
	return cmd
}

// auditPlan loads everything both audit verbs derive from: the config,
// the revised document, the operator inputs, and the upstream pin the
// observations are stamped with.
func auditPlan(dir, cfgFile string) (*config.Config, *plan.Plan, *specmodel.Document, store.Lock, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, nil, nil, store.Lock{}, err
	}
	if !cfg.Audit.Enabled {
		return nil, nil, nil, store.Lock{}, fmt.Errorf("audit.enabled is false in %s; nothing to do", cfgFile)
	}

	data, srcPath, err := auditSpecBytes(dir)
	if err != nil {
		return nil, nil, nil, store.Lock{}, err
	}
	doc, err := specmodel.Load(data)
	if err != nil {
		return nil, nil, nil, store.Lock{}, fmt.Errorf("%s: %w", srcPath, err)
	}

	lock, err := store.Verify(dir)
	if err != nil {
		return nil, nil, nil, store.Lock{}, err
	}

	rawInputs, err := os.ReadFile(plan.InputsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, store.Lock{}, err
	}
	inputs, err := plan.ParseInputs(rawInputs)
	if err != nil {
		return nil, nil, nil, store.Lock{}, err
	}

	p, err := plan.Derive(doc, cfg, inputs)
	if err != nil {
		return nil, nil, nil, store.Lock{}, err
	}
	return cfg, p, doc, lock, nil
}

// auditBaseURL picks the audited API's root: the flag, then the config
// override, then the document's first declared server.
func auditBaseURL(flag string, cfg *config.Config, dir string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if cfg.Audit.BaseURLOverride != "" {
		return cfg.Audit.BaseURLOverride, nil
	}
	if data, _, err := auditSpecBytes(dir); err == nil {
		if doc, err := specmodel.Load(data); err == nil && len(doc.Servers) > 0 && doc.Servers[0].URL != "" {
			return doc.Servers[0].URL, nil
		}
	}
	return "", fmt.Errorf("no base URL: the document declares no servers; pass --base-url or set audit.base_url_override")
}

// auditSpecBytes returns the document the audit interrogates against. It
// prefers dir/revised.yaml — the pinned upstream plus every accepted
// correction — so that a re-audit is informed by decisions already made.
// On a first run no correction has been accepted yet and `spec revise` has
// not materialised revised.yaml; the audit then reads the pinned upstream
// document directly, which is exactly what an unrevised spec is. It returns
// the bytes and the path they came from, for error messages.
func auditSpecBytes(dir string) ([]byte, string, error) {
	revisedPath := filepath.Join(dir, revise.OutputName)
	data, err := os.ReadFile(revisedPath) //nolint:gosec // the fixed name under the operator-supplied dir
	if err == nil {
		return data, revisedPath, nil
	}
	if !os.IsNotExist(err) {
		return nil, revisedPath, err
	}
	upstreamPath := filepath.Join(dir, store.DocumentName)
	data, err = os.ReadFile(upstreamPath) //nolint:gosec // the fixed name under the operator-supplied dir
	if os.IsNotExist(err) {
		return nil, upstreamPath, fmt.Errorf("neither %s nor %s exists; run `tfpfgen spec import` first", revisedPath, upstreamPath)
	}
	if err != nil {
		return nil, upstreamPath, err
	}
	return data, upstreamPath, nil
}

// auditLogger builds the run's zerolog logger. Requests log at debug;
// TFPFGEN_LOG_LEVEL=debug turns them on.
func auditLogger(w io.Writer) zerolog.Logger {
	level := zerolog.InfoLevel
	if v, ok := os.LookupEnv("TFPFGEN_LOG_LEVEL"); ok {
		if parsed, err := zerolog.ParseLevel(v); err == nil {
			level = parsed
		}
	}
	return zerolog.New(w).Level(level).With().Timestamp().Logger()
}

// printSummary renders the run's table.
func printSummary(w io.Writer, out string, obsCount int, sum auditrun.Summary) {
	fmt.Fprintf(w, "audit run %s: %d audited, %d blocked, %d exhausted, %d skipped by the plan\n",
		sum.RunID, sum.Audited, sum.Blocked, sum.TimedOut, sum.Skipped)
	for _, e := range sum.Entities {
		if e.Status == auditrun.StatusAudited {
			fmt.Fprintf(w, "  %-12s %s\n", e.Status, e.Entity)
			continue
		}
		fmt.Fprintf(w, "  %-12s %s: %s\n", e.Status, e.Entity, e.Reason)
	}

	fmt.Fprintf(w, "observations: %d written to %s\n", obsCount, out)
	kinds := make([]string, 0, len(sum.ByKind))
	for k := range sum.ByKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(w, "  %-20s %d\n", k, sum.ByKind[observe.Kind(k)])
	}

	fmt.Fprintf(w, "budget: %d/%d requests, %d objects created (ceiling %d), %s of %s\n",
		sum.Requests, sum.RequestBudget, sum.ObjectsCreated, sum.ObjectBudget,
		sum.Elapsed.Round(time.Millisecond), sum.DurationBudget)
	fmt.Fprintf(w, "cleanup: %d+%d removed at start, %d+%d at end (ledger+prefix)\n",
		sum.CleanupStart.LedgerDeletes, sum.CleanupStart.PrefixDeletes,
		sum.CleanupEnd.LedgerDeletes, sum.CleanupEnd.PrefixDeletes)
	for _, o := range append(append([]string{}, sum.CleanupStart.Orphans...), sum.CleanupEnd.Orphans...) {
		fmt.Fprintf(w, "  orphan: %s\n", o)
	}

	entities := make([]string, 0, len(sum.RejectsUnknownFields))
	for e := range sum.RejectsUnknownFields {
		entities = append(entities, e)
	}
	sort.Strings(entities)
	for _, e := range entities {
		if sum.RejectsUnknownFields[e] {
			fmt.Fprintf(w, "rejectsUnknownFields: %s true — this entity's refusal-based findings need caution\n", e)
			continue
		}
		fmt.Fprintf(w, "rejectsUnknownFields: %s false\n", e)
	}

	for _, a := range sum.Adjustments {
		gate := ""
		if a.GateField != "" {
			gate = fmt.Sprintf(" (gate %s=%s)", a.GateField, a.GateValue)
		}
		fmt.Fprintf(w, "adjustment: %s %s.%s%s\n", a.Action, a.Entity, a.Field, gate)
	}

	if sum.EdgesConfirmed > 0 || sum.EdgesInconclusive > 0 {
		fmt.Fprintf(w, "edges: %d confirmed, %d inconclusive\n", sum.EdgesConfirmed, sum.EdgesInconclusive)
	}
}
