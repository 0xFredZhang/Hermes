-- Deduplicate existing rows, keeping the earliest per aws_account_id,
-- then enforce one cloud account per AWS account id.
DELETE FROM cloud_accounts
WHERE id NOT IN (
    SELECT MIN(id) FROM cloud_accounts GROUP BY aws_account_id
);

CREATE UNIQUE INDEX idx_cloud_accounts_aws_account_id
    ON cloud_accounts (aws_account_id);
