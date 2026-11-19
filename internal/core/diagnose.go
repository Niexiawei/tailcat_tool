package core

// ProblemKind names a startup problem. The view uses it to offer the action
// that fixes it: opening the configuration directory, opening the tool
// settings, or nothing.
type ProblemKind int

const (
	// ProblemConfig is a configuration file that would not parse.
	ProblemConfig ProblemKind = iota
	// ProblemHistory is a history database that would not open.
	ProblemHistory
	// ProblemCredentials is a machine without a usable system credential
	// store.
	ProblemCredentials
	// ProblemAppMeta is the application-name cache that would not open.
	ProblemAppMeta
	// ProblemADB is no adb on the machine.
	ProblemADB
)

// Problem is one startup problem the user has to decide about.
type Problem struct {
	// Kind is which piece failed, and therefore which action fixes it.
	Kind ProblemKind
	// Title is the sentence the page shows.
	Title string
	// Detail is what the underlying failure said, empty when there was no
	// error behind it.
	Detail string
	// Path is the directory that holds what failed, empty when there is none.
	Path string
	// Continue is false only when the app cannot run at all.
	Continue bool
}

// DiagnoseInput is what main.go learned before the window opened. Diagnose
// never touches the disk: it only describes failures the caller already has,
// so that reporting them cannot itself fail, and so that a broken
// configuration is never written back while being reported.
type DiagnoseInput struct {
	ConfigDir      string
	ConfigErr      error
	HistoryErr     error
	CredentialsErr error
	AppMetaErr     error
	ADBPath        string
	ScrcpyPath     string
}

// Diagnose turns the startup failures into user-facing problems, in the order
// the user should fix them: the configuration first (it decides everything
// else), then what the app stores, then the tool it cannot connect without.
//
// Every problem it reports leaves the app usable, so all of them continue; a
// future failure that does not would set Continue false and the page would
// stop offering to carry on.
func Diagnose(in DiagnoseInput) []Problem {
	var problems []Problem
	if in.ConfigErr != nil {
		problems = append(problems, Problem{
			Kind:     ProblemConfig,
			Title:    "配置读取失败，已使用默认设置",
			Detail:   in.ConfigErr.Error(),
			Path:     in.ConfigDir,
			Continue: true,
		})
	}
	if in.HistoryErr != nil {
		problems = append(problems, Problem{
			Kind:     ProblemHistory,
			Title:    "历史库无法打开，本次运行不会保存历史",
			Detail:   in.HistoryErr.Error(),
			Path:     in.ConfigDir,
			Continue: true,
		})
	}
	if in.CredentialsErr != nil {
		problems = append(problems, Problem{
			Kind:     ProblemCredentials,
			Title:    "未检测到系统凭据库，连接地址仅本次运行有效",
			Detail:   in.CredentialsErr.Error(),
			Continue: true,
		})
	}
	if in.AppMetaErr != nil {
		problems = append(problems, Problem{
			Kind:     ProblemAppMeta,
			Title:    "应用信息缓存不可用，资源页只显示包名",
			Detail:   in.AppMetaErr.Error(),
			Path:     in.ConfigDir,
			Continue: true,
		})
	}
	if in.ADBPath == "" {
		problems = append(problems, Problem{
			Kind:     ProblemADB,
			Title:    "未找到 adb，连接设备前需要先安装",
			Continue: true,
		})
	}
	return problems
}
