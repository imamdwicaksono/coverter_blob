package database

import (
	"database/sql"
	"fmt"
)

func GetFolderRoleMaster(db *sql.DB) (*sql.Rows, error) {
	sql := `SELECT * FROM teradocu.folder_role_master frm`

	rows, err := db.Query(sql)
	if err != nil {
		fmt.Println("❌ Gagal menjalankan query:", err)
		return nil, err
	}

	return rows, nil
}
