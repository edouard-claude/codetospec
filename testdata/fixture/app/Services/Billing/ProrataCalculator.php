<?php

namespace App\Services\Billing;

use InvalidArgumentException;

class ProrataCalculator
{
    private const ROUNDING_PRECISION = 2;

    public function calculate(float $monthlyAmount, int $activationDay, int $daysInMonth): float
    {
        if ($monthlyAmount < 0) {
            throw new InvalidArgumentException('monthly amount must not be negative');
        }

        if ($activationDay <= 1) {
            return $monthlyAmount;
        }

        $remainingDays = $daysInMonth - $activationDay + 1;

        return round($monthlyAmount * $remainingDays / $daysInMonth, self::ROUNDING_PRECISION);
    }
}
