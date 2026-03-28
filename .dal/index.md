# Index

dal이 깨어나면 이 파일을 먼저 읽고, 필요한 것만 로드한다.

| DalID | 파일 | 설명 | 로드 시점 |
|---|---|---|---|
| — | index.md | 이 파일 | always |
| — | routing.md | 작업→dal 매핑 | leader Pre-Flight |
| — | roster.md | 팀 구성원+상태 | leader Pre-Flight |
| — | ceremonies.md | retrospective 규칙 | 실패 시 |
| — | team.md | 팀 설명 | 첫 세션 |
| — | config.json | 팀 설정 | always |
| DAL:WISDOM:a1b2c3d4 | wisdom.md | 팀 교훈 | Pre-Flight |
| DAL:DECISION:e5f6g7h8 | decisions.md | 아키텍처 결정 | Pre-Flight |
