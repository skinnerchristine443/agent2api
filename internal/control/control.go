package control

// Services 是控制台应用接口面，由 app.New 组装。
type Services struct {
	Accounts *Accounts
	Keys     *Keys
	Settings *Settings
	Backup   *Backup
	Catalog  *Catalog
	Usage    *Usage
}

func New(runtime Runtime) *Services {
	if runtime == nil {
		return nil
	}
	store := runtime.Store()
	return &Services{
		Accounts: NewAccounts(runtime),
		Keys:     NewKeys(store),
		Settings: NewSettings(store),
		Backup:   NewBackup(store),
		Usage:    usageFromStore(store),
	}
}
