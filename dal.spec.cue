package dalforge

// ===================================================
// dal.spec.cue — dalforge-hub 핵심 스펙 v1.0.0
//
// 하위 호환 정책:
//   major: 기존 .dalfactory 호환 깨짐 (마이그레이션 필수)
//   minor: 필드 추가만 허용 (기존 유효성 유지)
//   patch: 설명/주석만 변경
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

// ===== .dalfactory 템플릿 =====

// .dalfactory/templates/ 안에 여러 템플릿이 존재
// dalcenter join {template} 으로 인스턴스 생성
#DalTemplate: {
	schema_version!: #SemVer
	name!:           string & != ""
	description?:    string
	container!:      #ContainerSpec
	cli?: [...#DalID]
	skills?: [...#DalID]
	hooks?: [...#DalID]
	secrets?: [...#SecretEntry]
	permissions?: #Permissions
	compatibility?: #Compatibility
	health_check?: #HealthCheck
	build?: #BuildSpec
	exports?: [string]: #AgentExport
}

// ===== .dalfactory 루트 =====

// 레포 루트의 .dalfactory/ 폴더 정의
#DalFactory: {
	schema_version!: #SemVer
	dal!: {
		id!:       #DalID
		name!:     string & != ""
		version!:  #SemVer
		category!: #CategoryID
	}
	description?: string
	depends?: [...#Dependency]
	templates!: [string]: #DalTemplate
}

// ===== localdal (인형 인스턴스) =====

// localdal 하나 = dal 인형 하나
// .dalfactory 템플릿 기반으로 생성
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

#AuditAction: "join" | "install" | "update" | "remove" | "deprecate" | "retire" | "sync"
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
