package database

import (
	"context"
	"database/sql/driver"
	"fmt"
)

type sqliteDriverRows interface {
	driver.Rows
	driver.RowsColumnTypeDatabaseTypeName
	driver.RowsColumnTypeLength
	driver.RowsColumnTypeNullable
	driver.RowsColumnTypePrecisionScale
	driver.RowsColumnTypeScanType
}

type sqliteDeadlineRows struct {
	sqliteDriverRows
	conn *sqliteDeadlineConn
	ctx  context.Context
}

func (c *sqliteDeadlineConn) wrapRows(ctx context.Context, rows driver.Rows, err error) (driver.Rows, error) {
	if err != nil {
		if rows != nil {
			_ = rows.Close()
		}
		return nil, err
	}
	full, ok := rows.(sqliteDriverRows)
	if !ok {
		_ = rows.Close()
		return nil, fmt.Errorf("SQLite 驱动查询结果接口不兼容")
	}
	return &sqliteDeadlineRows{sqliteDriverRows: full, conn: c, ctx: ctx}, nil
}

func (r *sqliteDeadlineRows) Next(values []driver.Value) error {
	_, err := sqliteWithDeadline(r.conn, r.ctx, func() (struct{}, error) {
		return struct{}{}, r.sqliteDriverRows.Next(values)
	})
	return err
}

var _ sqliteDriverRows = (*sqliteDeadlineRows)(nil)
