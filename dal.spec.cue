package dalcenter

// ===================================================
// dal.spec.cue — dalcenter 스펙 v2.0.0
//
// localdal 기반 dal 관리. .dal/ 폴더에 dal.cue로 정의.
// ===================================================

// ===== 공통 타입 =====

#SemVer: =~"^[0-9]+\\.[0-9]+\\.[0-9]+$"
#Timestamp: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}"
#SHA256: =~"^sha256:[a-f0-9]{64}$"
#UUID: =~"^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$"

// ===== DAL ID 체계 =====

// 형식: DAL:{CATEGORY}:{uuid8}
// uuid8은 최초 발급 후 영구 고정, 재사용 금지
#DalID: =~"^DAL:[A-Z][A-Z0-9_]+:[a-f0-9]{8}$"
#CategoryID: =~"^[A-Z][A-Z0-9_]+$"

// ===== 카테고리 =====

#CategoryDef: {
	id!:          #CategoryID
	description!: string & != ""
	name_prefix!: string & =~"^dal[a-z]+-$"
}

builtin_categories: [Name=string]: #CategoryDef & {id: Name}
builtin_categories: {
	CLI: {
		id:          "CLI"
		description: "명령줄 도구"
		name_prefix: "dalcli-"
	}
	PLAYER: {
		id:          "PLAYER"
		description: "실행 환경"
		name_prefix: "dalplayer-"
	}
	CONTAINER: {
		id:          "CONTAINER"
		description: "컨테이너 서비스"
		name_prefix: "dalcontainer-"
	}
	SKILL: {
		id:          "SKILL"
		description: "에이전트 스킬"
		name_prefix: "dalskill-"
	}
	HOOK: {
		id:          "HOOK"
		description: "이벤트 훅"
		name_prefix: "dalhook-"
	}
}

// ===== 패키지 =====

#PackageStatus: "active" | "deprecated" | "retired"

#Package: {
	id!:          #DalID
	uuid!:        #UUID
	name!:        string & != ""
	category!:    #CategoryID
	version!:     #SemVer
	description!: string & != ""
	status!:      #PackageStatus
	checksum?:    #SHA256
	author?:      string
	aliases?: [...string]
	retired_by?: #DalID
	created_at!: #Timestamp
	updated_at?: #Timestamp
}

// ===== 의존성 =====

#Dependency: {
	id!:       #DalID
	version?:  string
	optional?: bool | *false
}

// ===== 빌드 =====

#BuildSpec: {
	language!: string & != ""
	entry!:    string & != ""
	output!:   string & != ""
	scripts?: {
		pre_build?:    string
		post_build?:   string
		pre_install?:  string
		post_install?: string
	}
}

// ===== 컨테이너 =====

#ContainerSpec: {
	base!:     string & != ""
	packages?: [...string]
	agents!: [string]: #AgentInstall
	expose?: [...int]
	volumes?: [...string]
	env?: [string]: string
}

#AgentInstall: {
	type!:    "claude_sdk" | "codex_appserver" | "gemini_cli" | "openai_compatible"
	package!: string & != ""
	model?:   string
}

// ===== 시크릿 =====

#SecretVault: {
	store_path!:  string & != ""
	keyring_path!: string & != ""
	algorithm!:   "aes-256-gcm"
}

#SecretEntry: {
	name!:       string & != ""
	sensitive!:  bool | *true
	required?:   bool | *false
	description?: string
}

// ===== 권한 =====

#Permissions: {
	filesystem?: [...string]
	network?:    bool | *false
	env_vars?: [...string]
	secrets?: [...string]
}

// ===== 호환성 =====

#Compatibility: {
	min_spec_version?: #SemVer
	os?: [...string]
	arch?: [...string]
}

// ===== 헬스체크 =====

#HealthCheck: {
	command!:  string & != ""
	interval?: string | *"30s"
	timeout?:  string | *"5s"
	retries?:  int | *3
}

#AgentExport: {
	skills?: [...string]
	hooks?:  [...string]
}

// ===== dal 프로필 =====

// .dal/<name>/dal.cue에 정의
// UUID로 식별, 폴더명은 별명
#DalProfile: {
	uuid!:        string & != ""
	name!:        string & != ""
	version!:     #SemVer
	player!:      "claude" | "codex" | "gemini"
	description?: string
	container?: #ContainerSpec
	skills?: [...string]
	hooks?:  [...string]
	exports?: [string]: #AgentExport
	budget?: #Budget
}

// ===== localdal (인형 인스턴스) =====

// localdal 하나 = dal 인형 하나
// .dal/<name>/dal.cue 기반으로 생성
#LocalDalStatus: "active" | "stopped" | "error" | "updating"

#LocalDal: {
	dal_id!:      #DalID
	node_id!:     string & != ""
	template!:    string & != ""
	status!:      #LocalDalStatus
	container_id?: string
	installed!: [...#InstalledPackage]
	vault!:       #SecretVault
	created_at!:  #Timestamp
	updated_at!:  #Timestamp
	last_error?:  string
}

#InstalledPackage: {
	id!:           #DalID
	version!:      #SemVer
	checksum?:     #SHA256
	installed_at!: #Timestamp
	path!:         string & != ""
}

// ===== PLAYER (인형 안의 에이전트) =====

#Player: {
	id!:       #DalID
	agent!:    string & != ""
	pid?:      int
	status!:   "running" | "idle" | "error"
	started_at?: #Timestamp
}

// ===== dalcenter 레지스트리 =====

#Registry: {
	schema_version!: #SemVer
	packages!: [string]: #Package
	categories!: [string]: #CategoryDef
}

// ===== 노드 인벤토리 =====

#NodeInventory: {
	node_id!:   string & != ""
	hostname?:  string
	dals!: [...#LocalDal]
	players?: [...#Player]
	last_sync!: #Timestamp
}

// ===== 동기화 =====

#SyncPolicy: {
	interval!:     string | *"5m"
	conflict!:     "center_wins" | "local_wins" | "manual"
	offline_mode!: "cache" | "reject" | "queue"
}

#SyncState: {
	last_sync_at?: #Timestamp
	status!:       "synced" | "pending" | "conflict" | "offline"
	pending_updates?: [...#DalID]
}

// ===== 감사 =====

#AuditAction: "wake" | "sleep" | "sync" | "update" | "remove" | "create" | "delete"
#AuditResult: "success" | "failure" | "skipped"

#AuditEvent: {
	id!:        string & != ""
	dal_id!:    #DalID
	action!:    #AuditAction
	result!:    #AuditResult
	actor?:     string
	node_id?:   string
	detail?:    string
	timestamp!: #Timestamp
}

// ===== Budget Guardrails =====

#BudgetPeriod: "hourly" | "daily" | "weekly" | "monthly"
#BudgetAction: "warn" | "pause" | "kill"

#BudgetLimit: {
	max_tokens?:   int & >0
	max_cost_usd?: number & >0
	max_requests?: int & >0
	max_prs?:      int & >0
	period!:       #BudgetPeriod
}

#BudgetAlert: {
	threshold_pct!: int & >=1 & <=100
	action!:        #BudgetAction
	notify?:        bool | *true
}

#Budget: {
	enabled!:   bool | *true
	limits!:    #BudgetLimit
	alerts?: [...#BudgetAlert]
	hard_stop!: bool | *true
}
