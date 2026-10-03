package deployment

import "context"

// validateManualTemporaryFiles 检查完整目标集合，不能因为历史临时路径列表缺项就忽略未知文件。
func validateManualTemporaryFiles(ctx context.Context, client Client, record TransactionRecord) error {
	for _, target := range record.Paths {
		path := target + ".scert-tmp-" + record.ID
		if exists, err := client.Exists(ctx, path); err != nil {
			return err
		} else if exists {
			if err := checkFileDigest(ctx, client, path, record.Evidence.TemporaryFiles[path]); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func validateRecoveryCommit(ctx context.Context, client Client, record TransactionRecord, allowMissingLocks bool) error {
	if err := validateTransactionLocks(ctx, client, record, allowMissingLocks); err != nil {
		return err
	}
	for _, path := range record.Paths {
		if err := checkFileDigest(ctx, client, path, record.Evidence.Files[path]); err != nil {
			return err
		}
	}
	if err := validateManualTemporaryFiles(ctx, client, record); err != nil {
		return err
	}
	for _, backup := range record.Backups {
		if _, err := validateRecoveryBackup(ctx, client, backup); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Service) executeRecoveryCommit(ctx context.Context, record *TransactionRecord, server Server, client Client, mutated *bool) error {
	allowMissingLocks := record.Phase == TransactionPhaseCommitVerified
	if err := validateRecoveryCommit(ctx, client, *record, allowMissingLocks); err != nil {
		return err
	}
	// 即使历史阶段已核验，也必须再次核验真实新证书，不能仅凭历史状态清理。
	if err := s.VerifyRecoveryTarget(ctx, server, record.Evidence.Baseline, true); err != nil {
		return err
	}
	if err := validateRecoveryCommit(ctx, client, *record, allowMissingLocks); err != nil {
		return err
	}
	*mutated = true
	for _, temporary := range record.TemporaryPaths {
		if err := client.Remove(ctx, temporary); err != nil {
			return err
		}
	}
	for _, backup := range record.Backups {
		if err := client.Remove(ctx, backup.Saved); err != nil {
			return err
		}
	}
	if err := s.persistRecoveryPhase(ctx, record, TransactionPhaseCommitVerified); err != nil {
		return err
	}
	return releaseTransactionLocks(ctx, client, *record, true, allowMissingLocks)
}

func validateRecoveredBaseline(ctx context.Context, client Client, record TransactionRecord) error {
	for _, backup := range record.Backups {
		if backup.OriginalExists {
			if err := checkFileDigest(ctx, client, backup.Original, backup.OriginalDigest); err != nil {
				return err
			}
		} else if exists, err := client.Exists(ctx, backup.Original); err != nil || exists {
			return ErrRecoveryActionRejected
		}
		// 还原完成后不允许留下新出现或未消费的临时文件、备份。
		for _, path := range []string{backup.Original + ".scert-tmp-" + record.ID, backup.Saved} {
			if exists, err := client.Exists(ctx, path); err != nil || exists {
				return ErrRecoveryActionRejected
			}
		}
	}
	return ctx.Err()
}

func (s *Service) executeRecoveryRollback(ctx context.Context, record *TransactionRecord, server Server, client Client, mutated *bool) error {
	allowMissingLocks := record.Phase == TransactionPhaseRollbackVerified
	if err := validateManualTemporaryFiles(ctx, client, *record); err != nil {
		return err
	}
	if allowMissingLocks {
		if err := validateTransactionLocks(ctx, client, *record, true); err != nil {
			return err
		}
		if err := validateRecoveredBaseline(ctx, client, *record); err != nil {
			return err
		}
	} else if err := validateRollbackFiles(ctx, client, *record); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	*mutated = true
	if !allowMissingLocks {
		if err := restoreTransactionFiles(ctx, client, *record); err != nil {
			return err
		}
	}
	for _, command := range [][]string{server.ValidationCommand, server.ReloadCommand} {
		response, err := client.Run(ctx, command)
		if err != nil || response.ExitCode != 0 {
			return ErrRecoveryActionUncertain
		}
	}
	if err := s.VerifyRecoveryTarget(ctx, server, record.Evidence.Baseline, false); err != nil {
		return err
	}
	if err := validateRecoveredBaseline(ctx, client, *record); err != nil {
		return err
	}
	if err := s.persistRecoveryPhase(ctx, record, TransactionPhaseRollbackVerified); err != nil {
		return err
	}
	return releaseTransactionLocks(ctx, client, *record, true, allowMissingLocks)
}
