#Player: "claude" | "codex" | "gemini"
#Role:   "leader" | "member"

// DalID: DAL:{CATEGORY}:{uuid8}
#Category: "CONTAINER" | "SKILL" | "WISDOM" | "DECISION" | "JOB" | "HOOK"

#BranchConfig: {
	base?: string | *"main"
}

#SetupConfig: {
	packages?: [...string]
	commands?: [...string]
	timeout?:  string | *"5m"
}

#DalProfile: {
	uuid!:           string & != ""
	name!:           string & != ""
	version!:        string
	player!:         #Player
	role!:           #Role
	skills?:         [...string]
	hooks?:          [...string]
	model?:          string
	player_version?: string
	auto_task?:      string
	auto_interval?:  string
	workspace?:      string
	branch?:         #BranchConfig
	setup?:          #SetupConfig
	description?:    string
	job?:            string // JOB UUID 참조
	git?: {
		user?:         string
		email?:        string
		github_token?: string
	}
}
