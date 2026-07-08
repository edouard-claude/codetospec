<?php

use Illuminate\Support\Facades\Route;

Route::post('/api/activate', [App\Http\Controllers\ActivationController::class, 'store']);
Route::get('/api/invoices', [App\Http\Controllers\InvoiceController::class, 'index']);
